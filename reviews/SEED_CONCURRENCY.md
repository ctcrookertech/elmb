# ELMB Seed — Concurrency & Deadlock Review

**Expert**: Marcus Chen, Concurrent Systems & Deadlock Analysis (22 years experience)
**Scope**: seed/machine.go, seed/cmd_elmb.go, seed/learn.go, seed/model.go, seed/build.go, core/out.go, infer/cmd_infer.go
**Date**: 2026-02-28

## Executive Summary

The ELMB state machine uses child process spawning (`exec.Command`) as its primary concurrency mechanism, which avoids shared-memory data races but introduces a distinct class of problems: process tree explosion, orphan processes, stdout corruption under concurrent writes, and indefinite blocking with no timeout or cancellation propagation. The codebase has reasonable depth guards (`maxLearnDepth=3`, `maxBuildDepth=2`) that limit recursive spawning, but several structural gaps remain that could cause production failures ranging from hung processes to corrupted output protocols.

## Findings

### 1. Deadlock Risks (machine.go: spawnAll, spawnSync)

| # | Finding | Severity | Recommendation |
|---|---------|----------|----------------|
| 1.1 | **No classical deadlock in spawnAll.** Each goroutine in `spawnAll` (line 301) calls `spawnSync` which blocks on `cmd.Output()`. There are no mutexes held across the `WaitGroup` boundary, and results/errors are written to pre-allocated per-index slots. The fan-out/fan-in pattern is structurally deadlock-free. | Info | None required. |
| 1.2 | **Nested spawning cannot deadlock but can livelock.** A child `elmb` process spawned by `spawnSync` is a separate OS process. It will run its own `Machine.Run()` loop, which may itself call `spawnAll` or `spawnSync`. Since these are independent OS processes (not goroutines sharing a fixed thread pool), there is no resource-bounded deadlock. However, if child processes wait on grandchild processes that themselves are waiting on network I/O that never completes, the entire tree livelocks indefinitely. | Medium | Add process-level timeouts (see finding 8.1). |

### 2. Process Tree Explosion (learn.go, build.go, machine.go)

| # | Finding | Severity | Recommendation |
|---|---------|----------|----------------|
| 2.1 | **Learn branching factor is bounded but exponential.** `processLearn` (learn.go:71) spawns 2 parallel infer calls via `spawnAll`. Each RECURSE directive creates a new Learn item at `Depth+1`. With `maxLearnDepth=3`, the worst case is `2^3 = 8` concurrent infer calls per original Learn item. But each infer call can return multiple RECURSE directives, so the branching factor per level is unbounded by the LLM response. At depth 0, N recurse directives each spawn 2 infer calls, each of which may return M recurse directives at depth 1, etc. The theoretical max process count is `O((2N)^3)` where N is the average number of RECURSE directives per response. | High | Add a per-item cap on RECURSE directives (e.g., `maxRecursePerItem = 3`). Consider a global concurrent-process semaphore. |
| 2.2 | **Build step spawning is sequential, not parallel, limiting blast radius.** `processBuild` (build.go:60) iterates steps sequentially, calling `spawnSync` for each actionable step. This is inherently self-throttling. However, failed steps are re-queued at `Depth+1` with the full error context as new content, which could be re-parsed into new steps. | Low | The `maxBuildDepth=2` guard is adequate for build. No change needed. |
| 2.3 | **No global process count limit.** There is no semaphore, rate limiter, or process counter across the entire machine. Multiple Learn items being processed (via the drain loop) combined with concurrent spawnAll calls could saturate the system. The drain loop itself is sequential (processes one mode at a time), but within a single `processLearn` call, the spawnAll launches 2 concurrent child processes, each of which runs its own Machine with its own spawning. | Medium | Implement a global `chan struct{}` semaphore (e.g., capacity 8) that each spawn must acquire before `exec.Command`. |

### 3. Goroutine Leaks in spawnAny (machine.go:321-347)

| # | Finding | Severity | Recommendation |
|---|---------|----------|----------------|
| 3.1 | **Goroutine leak after first result.** `spawnAny` reads one result from `ch` (line 345), then returns. The `defer cancel()` fires on return, cancelling `ctx`. The remaining goroutines are running `cmd.Output()` on a `CommandContext` cmd. When `ctx` is cancelled, Go sends `os.Kill` (SIGKILL on Linux) to the child process, which causes `cmd.Output()` to return an error. Each goroutine then sends to the buffered channel `ch` (capacity = `len(specs)`), so no goroutine blocks on send. **The goroutines themselves will terminate.** However, there is a timing window: if a goroutine's child process has already completed but the goroutine has not yet sent to `ch`, the context cancellation is a no-op and the goroutine proceeds normally, sending its result to the now-unread buffered channel. This is safe because the channel is buffered. | Low | Pattern is correct. Consider adding a `wg.Wait()` in a deferred goroutine to ensure all goroutines finish before the channel is garbage-collected, though in practice the buffered channel prevents blocking. |
| 3.2 | **spawnAny returns first result even if it is an error.** Line 345-346: `r := <-ch` returns whichever result arrives first. If the first completing goroutine errors (e.g., a fast process crash), `spawnAny` returns that error immediately, cancelling slower but potentially successful spawns. This is a semantic issue: "any" should arguably mean "first success." | Medium | Implement a loop over `ch` that skips errors, returning the first success, and only returning an error if all spawns fail. |

### 4. spawnAsync Handle Lifecycle (machine.go:383-416)

| # | Finding | Severity | Recommendation |
|---|---------|----------|----------------|
| 4.1 | **Orphaned handle leaks goroutines and processes.** If `spawnAsync` is called and the returned `*AsyncHandle` is dropped without calling `Cancel()`, the goroutines and child processes run to natural completion with no supervision. There is no finalizer, no timeout, and no automatic cleanup. | High | Add a `context.WithTimeout` as a fallback. Alternatively, document that `Cancel()` must be called (e.g., via a `defer h.Cancel()` pattern), or add a runtime finalizer that logs a warning. |
| 4.2 | **No current callers of spawnAsync.** Searching the codebase, `spawnAsync` is defined but never called in the reviewed files. This is dead code in the current implementation. | Low | Either remove it or add a comment marking it as reserved for future use. If kept, add the timeout safeguard from 4.1 before it gets used. |

### 5. Race Conditions on Machine State (machine.go, learn.go)

| # | Finding | Severity | Recommendation |
|---|---------|----------|----------------|
| 5.1 | **No shared Machine mutation from goroutines.** `spawnAll` (line 301) launches goroutines that each call `spawnSync`, which calls `exec.Command` and returns a string. The goroutines write to pre-allocated `results[i]` and `errs[i]` slots (distinct indices), so there is no data race. Machine state (`m.Stacks`, `m.Frames`) is only mutated after `spawnAll` returns (in `processLearn` lines 112-133). This is correct. | Info | None required. |
| 5.2 | **stdout interleaving from progress indicators is a data race on os.Stdout.** See finding 6.1. | High | See finding 6.1. |

### 6. Progress Indicator Races (core/out.go, machine.go)

| # | Finding | Severity | Recommendation |
|---|---------|----------|----------------|
| 6.1 | **Concurrent stdout writes create a data race.** `spawnAll` launches N goroutines. Each calls `spawnSync` (line 284), which calls `core.StartProgress()` (line 287). `StartProgress` (out.go:70) launches a goroutine that calls `fmt.Printf` and `fmt.Print(".")` on a ticker. With N concurrent progress goroutines plus the main goroutine potentially writing via `core.Line`, there are concurrent unsynchronized writes to `os.Stdout`. While `fmt.Print` calls are individually atomic at the `fmt` package level (they hold an internal lock on the `os.Stdout` writer), the interleaving of complete writes produces garbled output: `[progress] ....[progress] ....` on the same line. | High | This **corrupts the output protocol**. A parent process parsing stdout will see malformed lines that are neither valid `[progress]` tags nor valid `[output]` blocks. Options: (a) serialize all progress output through a mutex-protected writer; (b) suppress progress indicators in child spawns (child processes already have `--plain` so the children themselves are fine, but the parent's goroutines each start their own progress); (c) use a single shared progress indicator for the duration of `spawnAll`. |
| 6.2 | **`core.Newline()` after `stopProgress()` races with other goroutines.** In `spawnSync` (machine.go:296-297), `stopProgress()` is called followed by `core.Newline()`. If another goroutine's progress indicator is also printing dots to the same line at that moment, the newline splits another goroutine's progress output mid-line, creating a malformed progress tag. | High | Same fix as 6.1: serialize or unify progress output. |

### 7. exec.Command with stdin Piping (machine.go:248-262)

| # | Finding | Severity | Recommendation |
|---|---------|----------|----------------|
| 7.1 | **strings.NewReader as stdin is safe with cmd.Output().** `cmd.Output()` internally calls `cmd.Start()` and then `cmd.Wait()`. The `cmd.Start()` implementation copies stdin from the reader into the process's stdin pipe in a goroutine before the process starts reading. Since `strings.NewReader` provides all data immediately (it is entirely in memory), and the OS pipe buffer (typically 64KB on Linux) can hold reasonable prompt sizes, there is no deadlock risk for prompts under 64KB. | Low | If prompts could exceed 64KB (large frame contexts), the internal goroutine will block on the pipe write until the child reads. Since the child (infer) reads all stdin immediately via `io.ReadAll` (cmd_infer.go:25), this is safe. No issue for current usage, but worth noting for future prompt sizes. |
| 7.2 | **No stderr capture on child process failure.** `cmd.Output()` captures stdout but discards stderr. If a child process fails, the error returned is an `*exec.ExitError` which includes the exit code but the stderr content is lost. This makes debugging child failures difficult. | Medium | Use `cmd.CombinedOutput()` or set `cmd.Stderr = &stderrBuf` to capture diagnostic output from failed children. |

### 8. No Timeouts (machine.go: all spawn functions)

| # | Finding | Severity | Recommendation |
|---|---------|----------|----------------|
| 8.1 | **All spawn functions block indefinitely.** `spawnSync` (line 284), `spawnAll` (line 301), `runCommand` (line 270), `runCommandWithInput` (line 248), and `inferDirect` (line 264) all call `cmd.Output()` without any timeout. If a child process hangs (e.g., `infer` waiting on a network call that never responds, or a child `elmb` in an infinite recursion), the parent blocks forever. With `spawnAll`, if one of N children hangs, all N goroutines are blocked (via WaitGroup) and the entire machine stalls. | Critical | Use `exec.CommandContext` with a `context.WithTimeout` for all spawn operations. A reasonable default might be 5 minutes per child, with a configurable override. This is the single most impactful improvement. |
| 8.2 | **infer tool has no HTTP timeout.** `http.DefaultClient` in cmd_infer.go (line 49) has no timeout. If the Anthropic API hangs after accepting the connection, the infer process blocks forever, which cascades up to the parent elmb process. | Critical | Set `http.Client{Timeout: 2 * time.Minute}` or use a context-aware request with `http.NewRequestWithContext`. |

### 9. Context Cancellation in spawnAny (machine.go:321-347)

| # | Finding | Severity | Recommendation |
|---|---------|----------|----------------|
| 9.1 | **exec.CommandContext sends SIGKILL, not SIGTERM.** Go's `exec.CommandContext` (used in `spawnAny` line 333 and `spawnAsync` line 397) sends `os.Kill` (SIGKILL on Linux) when the context is cancelled. SIGKILL cannot be caught, so the child process gets no chance to clean up. If the child `elmb` has itself spawned grandchildren (via its own `spawnSync` or `spawnAll`), those grandchildren become orphaned processes (reparented to PID 1) and continue running. | High | Use `cmd.Cancel` (Go 1.20+) or `cmd.WaitDelay` to send SIGTERM first, then SIGKILL after a grace period. Alternatively, use process groups (`cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}`) and kill the entire process group on cancellation. |
| 9.2 | **Orphaned grandchildren from SIGKILL are invisible.** When a child `elmb` is killed, its grandchild processes (infer HTTP calls, other elmb instances) keep running. These consume API quota, hold network connections, and may produce output to nowhere. There is no process tree tracking. | High | Implement process group management. Set `Setpgid: true` on all child processes and kill the entire process group (`-pid`) on cancellation. This ensures the full subtree is terminated. |

## Overall Assessment

**Grade**: C+

The codebase demonstrates a clear architectural vision: using process spawning rather than in-process goroutines for the ELMB mode processing isolates failure domains and avoids shared-memory complexity. The depth guards (`maxLearnDepth`, `maxBuildDepth`) show awareness of recursion risks, and the `spawnAll` fan-out pattern is correctly implemented from a data-race perspective with per-index result slots and a simple WaitGroup barrier.

However, the absence of timeouts across every spawn path (finding 8.1, 8.2) is a critical gap that means any network hiccup or API stall causes the entire process tree to hang indefinitely with no recovery mechanism. The stdout corruption from concurrent progress indicators (finding 6.1, 6.2) is not merely cosmetic -- it breaks the structured output protocol that parent processes rely on for parsing child output, which means `parseOutputBlock` in a parent could silently return incorrect or empty results. The SIGKILL behavior on cancellation (finding 9.1, 9.2) creates orphan process subtrees that are invisible to the operator. Finally, the unbounded RECURSE branching in Learn mode (finding 2.1), while depth-limited, has no breadth limit and could spawn dozens of concurrent API calls from a single input.

The code is at an early stage where these issues are tolerable for development and testing, but each of findings 8.1, 8.2, 6.1, 9.1, and 2.1 would need to be resolved before any production or semi-autonomous deployment.

## Recommendations Summary

1. **[Critical] Add timeouts to all spawn and HTTP operations.** Use `exec.CommandContext` with `context.WithTimeout` in `spawnSync`, `runCommand`, and `runCommandWithInput`. Set an explicit `http.Client.Timeout` in `infer/cmd_infer.go`. This is the highest-priority fix.

2. **[High] Implement process group management for child processes.** Set `Setpgid: true` on all spawned commands and kill process groups on cancellation or timeout. This prevents orphaned grandchild processes.

3. **[High] Serialize or unify progress indicator output.** Either (a) use a single progress indicator for the duration of `spawnAll` instead of one per goroutine, or (b) protect all stdout writes with a shared mutex. This preserves the output protocol integrity.

4. **[High] Cap RECURSE breadth in Learn mode.** Add a `maxRecursePerItem` constant (suggested value: 3-5) to limit the number of RECURSE directives processed per Learn item, preventing LLM-driven process tree explosion.

5. **[Medium] Change spawnAny to return first success, not first completion.** Loop over the result channel, skipping errors, and only return an error if all spawns fail.

6. **[Medium] Capture stderr from child processes.** Set `cmd.Stderr` to a buffer and include it in error messages for debuggability.

7. **[Medium] Add a global concurrent-process semaphore.** Use a buffered channel (e.g., capacity 8-16) as a semaphore that all spawn functions must acquire before launching a child process.

8. **[Low] Remove or guard spawnAsync.** It is currently dead code. Either delete it or add a timeout-based context and document the `Cancel()` contract before it gets used.
