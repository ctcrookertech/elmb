# ELMB Seed — Reliability & Fault Tolerance Review

**Expert**: James Okoro, Systems Reliability & Fault Tolerance (24 years experience)
**Scope**: seed/machine.go, seed/cmd_elmb.go, seed/learn.go, seed/model.go, seed/build.go, core/out.go, infer/cmd_infer.go
**Date**: 2026-02-28

## Executive Summary

The ELMB state machine implements a four-mode processing pipeline (Enact, Learn, Model, Build) with LLM-driven control flow, child process spawning, and unbounded frame growth. The system has **no circuit breakers, no budgets, and no timeout enforcement**, meaning a single adversarial or confused LLM response can trigger unbounded resource consumption across API calls, memory, and child processes. The error handling philosophy is inconsistent across modes: Enact silently drops failures, Learn and Model arise on error (potentially propagating garbage), and Build silently discards failed steps. Combined with the lack of idempotency and process group management, this system is unsuitable for unattended production use in its current form.

## Findings

### 1. Infinite Loop Potential (machine.go, learn.go, model.go)

| # | Finding | Severity | Recommendation |
|---|---------|----------|----------------|
| 1.1 | **Learn-Model infinite cycle.** Model's `INVESTIGATE` directive relaxes an item back to Learn (model.go:82). Learn always arises to Model (learn.go:144). If the LLM consistently outputs `INVESTIGATE`, this creates an unbounded Learn-Model loop. The `maxLearnDepth=3` guard in learn.go:126 only governs `RECURSE` directives within Learn itself — it does **not** apply to items relaxed from Model. Items arriving from Model have `Source: core.Model` but `Depth` is not incremented on the relax path (model.go:82 creates a new Item with default `Depth: 0`). This resets the depth counter, defeating the recursion guard entirely. | **Critical** | Introduce a global cycle counter or a `ModelRelaxCount` field on Item. Cap the total number of Learn-Model transitions (e.g., 5). When the cap is reached, force a DONE and arise the item with whatever context exists. |
| 1.2 | **Learn RECURSE depth is per-item, not global.** Each new RECURSE item inherits `item.Depth + 1` (learn.go:128), but the depth only tracks the linear chain from a single root. If the LLM outputs multiple RECURSE directives at each level, the branching is exponential: up to `R^3` total learn items where `R` is the number of RECURSE directives per response. At `maxLearnDepth=3` with 3 RECURSE per level, that is 39 learn items (each making 2 API calls). | **High** | Add a global learn-item budget to the Machine. Decrement it on each processLearn entry. When exhausted, skip RECURSE directives and proceed to arise. |
| 1.3 | **drain() processes a mode's stack until empty, but processing pushes to the same stack.** Learn pushes RECURSE items to `Stacks[ModeLearn]` (learn.go:128). Model's INVESTIGATE relaxes to Learn, which eventually arises back to Model. Since drain() loops on `len(m.Stacks[mode]) > 0` (machine.go:134), any self-feeding within a mode prevents drain from ever returning for that mode. | **High** | Consider draining in batches: pop a snapshot of the current stack, process it, then check for new items. This provides a natural point to insert budget checks. |

### 2. Error Handling Inconsistency (machine.go, learn.go, model.go, build.go)

| # | Finding | Severity | Recommendation |
|---|---------|----------|----------------|
| 2.1 | **processEnact silently drops items on error.** When `runCommand` fails (machine.go:179), the error is logged but the item is neither arisen nor relaxed. It simply vanishes from the system. If the seed action fails, the machine terminates with an empty frame — no error propagation to the caller. | **High** | On Enact failure, either: (a) push a structured error item to Learn so the system can reason about the failure, or (b) set a Machine-level error state and halt. At minimum, set a non-zero exit code. |
| 2.2 | **Learn arises on error with the original item.** When `spawnAll` fails (learn.go:103-106), the original item is arisen to Model unchanged. Model then plans based on raw command output that was never analyzed. This means Model operates on unprocessed data, producing plans based on content it was not designed to interpret. | **Medium** | On Learn failure, either retry with backoff, or tag the item with an error flag so Model can detect unprocessed input and handle it differently (e.g., skip planning, emit DONE). |
| 2.3 | **Model arises on error with the original item.** When `inferDirect` fails (model.go:64-66), the original item is arisen to Build. Build then receives raw observations instead of a structured plan with STEP directives. `parseBuildSteps` will find no steps (build.go:50) and add the raw content to the frame. The data is not lost, but it is never acted on. | **Medium** | Tag items with a processing-failure flag. Or, when Model fails, push to frame directly with a "model-failed" annotation instead of arising to Build, where the item will be silently absorbed with no useful outcome. |
| 2.4 | **Build silently drops failed steps.** When a child spawn fails (build.go:77-79), the failure is logged and added to a `failures` list, but processing continues to the next step. Failed steps are only retried if `item.Depth < maxBuildDepth`, and only as a batch re-queue of the entire plan with error context appended (build.go:86-94). Individual step failures are not distinguishable from the original steps in the re-queue. | **Medium** | Track which specific steps failed and only re-queue those. The current approach re-parses the entire plan content on retry, which may re-execute steps that already succeeded. |
| 2.5 | **cmd_elmb.go does not exit with a non-zero code on failure.** `main()` calls `m.Run()` and exits normally regardless of whether any processing succeeded. An operator or parent process cannot distinguish a successful run from one where every step failed. | **High** | Track whether any error occurred during Run(). Exit with code 1 if the machine completed with unresolved errors. |

### 3. Resource Exhaustion (learn.go, model.go, build.go, infer/cmd_infer.go)

| # | Finding | Severity | Recommendation |
|---|---------|----------|----------------|
| 3.1 | **Worst-case API call count is unbounded due to finding 1.1.** Setting aside the infinite loop, even in the bounded case: Learn makes 2 calls per item + 1 for compaction (when triggered). Model makes 1. Build makes N (one per actionable step). With `maxLearnDepth=3` and branching factor R, Learn alone produces `2 * sum(R^k, k=0..3)` calls. With R=3: 80 Learn API calls, plus Model calls, plus Build calls per step. A 10-step plan yields an additional 10 child elmb spawns, each potentially running their own Learn/Model chain. | **Critical** | Implement a global API call budget on the Machine. Decrement on each `inferDirect` and `spawnSync` call. When exhausted, gracefully finalize with whatever frame state exists. |
| 3.2 | **No timeout on API calls.** `infer/cmd_infer.go` uses `http.DefaultClient.Do(req)` (line 49) with no timeout. If the Anthropic API hangs, the process blocks forever. The parent elmb process uses `exec.Command` without a context timeout, so the parent also blocks forever. | **Critical** | Set an `http.Client` with a 60-120 second timeout in infer. Use `exec.CommandContext` with a deadline in the parent for all child process invocations. |
| 3.3 | **No rate limiting.** The system fires API calls as fast as the network allows. With parallel spawns via `spawnAll` (learn.go:102) and concurrent build steps, the system can easily hit Anthropic API rate limits, causing cascading 429 errors that are treated as fatal failures. | **High** | Add a semaphore or token-bucket rate limiter before API calls. At minimum, detect 429 responses in infer and retry with exponential backoff. |
| 3.4 | **infer does not report HTTP response body on error.** When `resp.StatusCode != 200` (infer/cmd_infer.go:58-63), only the status code is logged. The Anthropic API returns detailed error messages in the response body (rate limit info, overloaded status, invalid request details). This information is discarded. | **Medium** | Read and log the response body on non-200 status codes. This is essential for diagnosing rate limits vs. auth failures vs. server errors. |

### 4. Process Orphaning (machine.go)

| # | Finding | Severity | Recommendation |
|---|---------|----------|----------------|
| 4.1 | **No process group management.** Child processes are spawned via `exec.Command` (machine.go:251, 272, 288) without setting `SysProcAttr.Setpgid`. If the parent is killed with SIGKILL, children continue running and consuming API quota. `spawnAny` (machine.go:321-347) is particularly dangerous: it cancels the context, but `exec.CommandContext` only sends SIGKILL on context cancellation — it does not clean up grandchild processes spawned by the child elmb. | **High** | Set `cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}` and kill the process group on cleanup. Register a signal handler in the parent to clean up all child process groups on SIGTERM/SIGINT. |
| 4.2 | **spawnAny leaks goroutines.** When the first result arrives (machine.go:345), the context is cancelled via `defer cancel()`. The remaining goroutines will eventually terminate when `cmd.Output()` returns an error from context cancellation. However, if a child process ignores the SIGKILL (zombie), the goroutine blocks indefinitely. The channel `ch` has capacity `len(specs)`, so it will not block on send, but the goroutines themselves leak. | **Medium** | Track goroutines and ensure they complete within a bounded time after context cancellation. Log a warning if they do not. |
| 4.3 | **spawnAsync has no cleanup pathway.** The `AsyncHandle` returned by `spawnAsync` (machine.go:383-416) provides `Cancel()` but no `Wait()` method. If the Machine's `Run()` loop completes while async children are still running, the process exits and orphans them. Currently `spawnAsync` does not appear to be called from the processing pipeline, but its existence as a public method invites future misuse. | **Low** | Add a `Wait()` method. Require all async handles to be waited on before `Run()` returns. Or remove `spawnAsync` if it is not used. |

### 5. Memory Growth (machine.go, learn.go)

| # | Finding | Severity | Recommendation |
|---|---------|----------|----------------|
| 5.1 | **Frame grows without bound.** Every Learn iteration pushes entries via `framePush` (learn.go:116). Compaction is triggered only when `len(f) > compactThreshold` (learn.go:136), but compaction itself is best-effort: if `inferDirect` fails (learn.go:184-187), the frame is left as-is and continues to grow. After N Learn cycles, the frame contains N+ entries. Each Learn call includes the full frame text in its prompt (learn.go:78), so prompt size grows linearly. At some point, the prompt exceeds the LLM's context window (the API hard-caps at `max_tokens=4096` for output, but input can grow to model limits). | **High** | Enforce a hard cap on frame size. If compaction fails, apply a simple truncation policy (drop oldest LevelProc entries). Track total frame bytes and halt or warn when approaching model context limits. |
| 5.2 | **Item.Content holds full LLM responses.** Each arise/relax copies the full content string through the stack. Model passes the entire plan (with all STEPs) as `Item.Content` (model.go:101-105). Build re-parses this content. A plan with many steps can be a multi-kilobyte string duplicated across stack entries, frame entries, and prompt constructions. | **Medium** | Consider a content-addressed store (map of hash to content) with Items holding only references. This bounds memory to unique content rather than copies. |
| 5.3 | **parseOutputBlock accumulates all stdout in memory.** Both `runCommand` and `runCommandWithInput` (machine.go:270-280, 248-262) call `cmd.Output()` which buffers the entire child stdout. If a child process produces unbounded output, the parent's memory grows accordingly. | **Medium** | Set a maximum read limit on child stdout. Use `io.LimitReader` on the command's stdout pipe instead of `cmd.Output()`. |

### 6. Partial Failure in spawnAll (machine.go, learn.go)

| # | Finding | Severity | Recommendation |
|---|---------|----------|----------------|
| 6.1 | **spawnAll returns partial results plus the first error.** The error loop (machine.go:313-318) returns the first non-nil error but also returns the `results` slice, which contains valid results for successful spawns and empty strings for failed ones. Learn (learn.go:102-107) checks only the error, and on error, arises the original item without examining partial results. Valid extract results from a successful first spawn are silently discarded. | **Medium** | On partial failure, process the successful results before arising. Or, run the spawns sequentially so a failure in one can be handled before the other is attempted. |
| 6.2 | **spawnAll returns an arbitrary error, not necessarily the most informative one.** The loop iterates `errs` in index order and returns the first non-nil. If spawn 0 succeeds but spawn 1 fails, the error from spawn 1 is returned. If both fail, only spawn 0's error is returned. There is no aggregation. | **Low** | Use `errors.Join` (Go 1.20+) to combine all errors, or return a structured result type that pairs each output with its error. |

### 7. Exit Code Propagation (machine.go, infer/cmd_infer.go)

| # | Finding | Severity | Recommendation |
|---|---------|----------|----------------|
| 7.1 | **Child stderr is not captured.** `exec.Command.Output()` captures stdout but not stderr. When a child process fails, `*exec.ExitError` contains `Stderr` only if the command was run with `cmd.Stderr` set (it is not). The logged error message is just "exit status 1" with no diagnostic information from the child. | **High** | Use `cmd.CombinedOutput()` or explicitly set `cmd.Stderr = &bytes.Buffer{}` and include its contents in error messages. Alternatively, since the output protocol uses stdout for everything, pipe stderr to a buffer and log it on failure. |
| 7.2 | **infer exits with code 1 on HTTP errors but not on empty responses.** If the API returns 200 but produces no content_block_delta events, infer writes `[exoutput]` with no content between the block markers (infer/cmd_infer.go:97-101). The parent receives an empty string, which is valid parse-wise but semantically represents a failure. | **Medium** | Detect empty responses in infer and exit with a distinct non-zero code (e.g., 2). Or, emit a structured error marker in the output block. |
| 7.3 | **infer ignores json.Marshal errors.** Line 35-36 of cmd_infer.go discards the error from `json.Marshal`. While this particular call cannot fail (all values are serializable), suppressing the error sets a dangerous precedent and will mask real failures if the structure evolves. | **Low** | Check the error. |

### 8. Frame Corruption from Compaction (learn.go)

| # | Finding | Severity | Recommendation |
|---|---------|----------|----------------|
| 8.1 | **Overlapping ranges corrupt frame indices.** Compaction applies `=` directives in reverse order (learn.go:191), which is correct only if ranges do not overlap. If the LLM produces `= 0-5: ...` and `= 3-8: ...`, applying `= 3-8` first changes the frame length, making the `= 0-5` range point at different elements than intended. The reverse-order strategy assumes non-overlapping, descending ranges — an assumption that is never validated and depends entirely on LLM behavior. | **High** | Sort directives by `Low` descending and validate that no ranges overlap before applying. If overlaps are detected, skip the compaction entirely (log a warning) or merge the overlapping ranges. |
| 8.2 | **Out-of-bounds check uses stale frame length.** The bounds check (learn.go:196) reads `len(m.Frames[name])` on each iteration, which reflects mutations from previous iterations in the same loop. After applying one replacement, the frame is shorter, but subsequent directives were generated against the original frame. Their indices are now wrong even if they were originally valid and non-overlapping. | **High** | Snapshot the original frame length before the loop. Apply all directives to a copy of the original frame, building the result in a single pass. Or, validate all directives against the original frame dimensions and reject the entire batch if any are invalid post-application. |
| 8.3 | **Compaction can increase frame size.** If the LLM produces a `=` directive where the replacement text is longer than the combined text of the replaced entries, the frame grows. There is no check that compaction actually reduces entry count or total size. Pathological LLM output could cause compaction to run every cycle and grow the frame each time. | **Low** | After compaction, verify that `len(m.Frames[name]) < original length`. If not, revert to the pre-compaction frame and log a warning. |

### 9. No Idempotency (machine.go, learn.go, model.go)

| # | Finding | Severity | Recommendation |
|---|---------|----------|----------------|
| 9.1 | **No checkpointing or journaling.** The Machine holds all state in memory with no serialization. If the process crashes mid-execution, all progress is lost. A restart re-runs the seed command from scratch, re-executing all API calls and re-spawning all children. | **High** | Implement checkpoint serialization: after each drain cycle, write Machine state (Stacks + Frames) to a JSON file. On startup, check for a checkpoint and resume from it. |
| 9.2 | **Frame entries are duplicated on replay.** Since Learn pushes to the frame (learn.go:116) and Model pushes to the frame (model.go:86, 91), re-processing the same item produces duplicate frame entries. There is no deduplication or content-based identity for frame elements. | **Medium** | Add a content hash or sequence ID to FrameElement. Deduplicate on push, or use the checkpoint mechanism to avoid re-processing. |
| 9.3 | **API calls are not cached.** Identical prompts sent to `inferDirect` produce independent API calls. If the same Learn item is processed twice (due to restart or the Learn-Model cycle), the same prompt is sent again, consuming additional quota and potentially producing different results (LLM non-determinism). | **Medium** | Implement a prompt-hash-to-response cache with a configurable TTL. This also helps with the rate limiting issue (finding 3.3). |

### 10. Additional Findings (core/out.go, machine.go)

| # | Finding | Severity | Recommendation |
|---|---------|----------|----------------|
| 10.1 | **StartProgress is not thread-safe with other output.** The progress goroutine (core/out.go:72-84) writes dots to stdout concurrently with other output. Since `fmt.Printf` and `fmt.Print` are not atomic at the line level, progress dots can interleave with tagged output lines, corrupting the output protocol. With `spawnAll` running two progress indicators simultaneously, interleaving is likely. | **Medium** | Use a mutex around all stdout writes in the core package. Or, write progress to stderr (which the convention reserves for future use — this is that future). |
| 10.2 | **parseOutputBlock ANSI stripping is fragile.** The manual ANSI escape parser (machine.go:431-443) only handles `ESC[...m` sequences. Other escape sequences (cursor movement, erase, OSC) will pass through and potentially corrupt parsed output. | **Low** | Use a proper ANSI stripping regex: `\033\[[0-9;]*[a-zA-Z]` at minimum. Or strip all sequences matching `\033[^m]*m` which the current code approximates but does not handle edge cases (e.g., missing `m` terminator causes infinite scan). |
| 10.3 | **siblingPath calls os.Exit(1) on failure.** `siblingPath` (machine.go:421-427) calls `os.Exit(1)` if it cannot determine the executable path. This bypasses all deferred cleanup and prevents the caller from handling the error. In a system with child processes and async handles, an abrupt exit leaks resources. | **Medium** | Return an error from `siblingPath` and propagate it. Let the caller decide whether to exit. |

## Overall Assessment

**Grade: D+ (Proof-of-concept, not production-ready)**

The ELMB state machine demonstrates a creative architecture for LLM-driven recursive task decomposition. The four-mode pipeline (Enact, Learn, Model, Build) with arise/relax dynamics is conceptually sound and the code is readable. However, the system has fundamental reliability deficiencies that make it dangerous to run unattended or at any meaningful scale.

The most critical issue is the **absence of any termination guarantee**. The Learn-Model cycle has no global bound, the Learn RECURSE branching is exponential within its depth limit, and the Build retry mechanism re-queues entire plans. Combined with **no API call budget, no timeouts, and no rate limiting**, a single confused LLM response can trigger runaway resource consumption — burning API quota, spawning unbounded child processes, and growing memory without limit. The error handling is inconsistent across modes in a way that masks failures: Enact drops items silently, Learn and Model propagate garbage upward, and Build silently absorbs failed steps. An operator watching the output stream has no reliable way to distinguish a healthy run from one that has entered a failure spiral. The process management story is incomplete: no process groups, no signal handling, no cleanup of orphaned children. There is no crash recovery, no checkpointing, and no idempotency — every restart is a full re-execution from scratch.

## Recommendations Summary

Ordered by priority (critical items first):

1. **Add a global API call budget** with a configurable cap (e.g., 50 calls). Decrement on every `inferDirect` and `spawnSync`. Gracefully finalize when exhausted. This single change caps the blast radius of every other issue.

2. **Break the Learn-Model infinite cycle.** Add a `RelaxCount` or `CycleCount` field to Item. Increment on every relax. Cap at 3-5 transitions. When reached, force DONE and arise.

3. **Add timeouts to all external calls.** Set `http.Client.Timeout` in infer. Use `exec.CommandContext` with deadlines for all child process invocations in machine.go.

4. **Set process groups and register signal handlers.** Use `Setpgid` on child processes. On SIGTERM/SIGINT, kill all child process groups before exiting.

5. **Capture child stderr on failure.** Set `cmd.Stderr` to a buffer and include it in error messages. This is the single highest-leverage debugging improvement.

6. **Enforce frame size limits.** Hard-cap frame entries (e.g., 50). If compaction fails and the frame is at capacity, drop the oldest LevelProc entries.

7. **Fix compaction overlap corruption.** Validate that ranges are non-overlapping and apply against a snapshot of the original frame. Reject the batch if validation fails.

8. **Make error handling consistent.** Define a clear policy: either all modes propagate errors upward (with an error tag), or all modes drop-and-log. The current mix creates unpredictable behavior.

9. **Propagate exit codes.** Track errors in Machine. Exit with non-zero status when the run completed with unresolved errors.

10. **Add checkpointing.** Serialize Machine state after each drain cycle. Resume from checkpoint on restart. This provides crash recovery and prevents duplicate API calls.

11. **Add rate limiting.** Implement a semaphore or token bucket before API calls. Detect and retry on HTTP 429 with exponential backoff in infer.

12. **Serialize stdout writes.** Add a mutex around all `fmt.Print`/`fmt.Printf` calls in core/out.go to prevent progress dot interleaving.
