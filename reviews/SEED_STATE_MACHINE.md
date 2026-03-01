# ELMB Seed -- State Machine & Automata Review

**Expert**: Dr. Helena Voss, State Machine & Automata Theory (25 years experience)
**Scope**: seed/machine.go, seed/cmd_elmb.go, seed/learn.go, seed/model.go, seed/build.go, core/out.go, infer/cmd_infer.go
**Date**: 2026-02-28

## Executive Summary

The ELMB state machine implements a four-mode processing pipeline (Enact, Learn, Model, Build) with stack-based work queues, arise/relax transitions, and recursive child spawning. The core loop is structurally sound for simple linear progressions, but contains several semantic issues that compromise the intended adaptive feedback loop. Most critically, the drain-then-scan design, combined with early returns in processModel and a parseBuildSteps mismatch on re-queued failures, means the full "enact, learn, model, build, re-enact" cycle described in the design intent will not reliably complete.

## Findings

### 1. E->L->M->B State Progression (machine.go, learn.go, model.go, build.go)

| # | Finding | Severity | Recommendation |
|---|---------|----------|----------------|
| 1.1 | **Forward progression works for the linear case.** processEnact runs a command, arises to Learn. processLearn analyzes and arises to Model. processModel plans and arises to Build. This chain holds when the LLM produces clean, expected directive output. The happy path is correct. | Informational | None needed. |
| 1.2 | **No re-enact path exists.** processBuild spawns child `elmb` processes with `--limit model` but never relaxes back down to Enact. Once Build completes, no mechanism pushes a new command onto the Enact stack. The described scenario -- "build creates new tools, then re-enacts" -- has no implementation. Build adds results to the frame and terminates. The full adaptive loop is incomplete. | Critical | processBuild (or a post-build phase) must be able to relax or directly push items onto Stacks[ModeEnact] when the plan includes re-execution steps. Consider adding a `REENACT: <command> <args>` directive type in the build phase. |
| 1.3 | **processEnact swallows errors silently.** When runCommand returns an error, processEnact logs it and returns without arising. The error output is lost -- it is neither placed on the frame nor arisen to Learn for analysis. This is exactly the scenario where learning from failure is most valuable. | High | On error, arise an Item containing the error text so Learn can analyze the failure and Model can plan recovery. |

### 2. Arise/Relax Semantics (machine.go, model.go)

| # | Finding | Severity | Recommendation |
|---|---------|----------|----------------|
| 2.1 | **Model INVESTIGATE relax to Learn works structurally.** processModel calls `m.relax(ModeModel, item)` which pushes onto Stacks[ModeLearn]. However, drain() is currently draining ModeModel. The relaxed item lands on ModeLearn, which has a lower index. The outer Run() loop will not see it until ModeModel's stack is fully drained (since Run scans from ModeEnact upward and breaks on the first non-empty stack). So the relaxed item will be processed, but only after the current Model drain completes. This is correct but non-obvious -- it means Model items queued after the INVESTIGATE return are lost (see 3.1). | Medium | Document this ordering guarantee. Consider whether draining should be interruptible. |
| 2.2 | **Relax from Learn is impossible.** Learn never calls relax(). If Learn's assessment determines that a new command should be run, there is no mechanism to push back to Enact. Learn can only arise to Model or recurse within itself. | High | Add a `REENACT: <command>` directive type in Learn's assess phase that relaxes to Enact, or allow Learn to directly push onto Stacks[ModeEnact]. |
| 2.3 | **Arise at limit deposits to frame, not output.** When `from >= m.Limit`, arise() adds to the frame and returns. This is correct for `--limit learn` (Learn arises, hits limit, deposits to frame). However, the deposited item's Content may be a summary string rather than actionable output, which could confuse consumers expecting structured output. | Low | Clarify the contract of what frame content looks like at different limit levels. |

### 3. Stack-Based LIFO Processing and drain() Semantics (machine.go)

| # | Finding | Severity | Recommendation |
|---|---------|----------|----------------|
| 3.1 | **LIFO ordering inverts processing order.** Items are appended to stacks and popped from the end. When processLearn produces multiple RECURSE items, they are all appended in order but processed in reverse. For the Learn phase this is mostly cosmetic (each recurse is independent), but in Build, the STEP ordering matters -- steps may have sequential dependencies (e.g., "create file" then "compile file"), and LIFO reversal will execute them backwards. | High | Either use FIFO (pop from index 0, or use a proper queue), or reverse the order of appended items. For Build steps specifically, they are iterated in a for loop within processBuild so this does not apply to the inner loop, but it does apply if multiple Build items land on the stack from different Model calls. |
| 3.2 | **drain() fully exhausts one mode before the outer loop re-scans.** This means if processModel relaxes an item to Learn, that Learn item waits until the entire Model stack is drained. This is by design but creates a subtle issue: the relaxed Learn item may be stale by the time it runs because the Model drain may have mutated the frame significantly in the interim. | Medium | Consider breaking out of drain() after a relax operation so the outer loop can process the lower-priority mode first. This would give relax true priority semantics. |
| 3.3 | **drain() processes items added during iteration.** If processLearn pushes new items onto Stacks[ModeLearn] (RECURSE), the drain loop's `len(m.Stacks[mode]) > 0` condition will pick them up immediately. This is correct behavior but means a single drain(ModeLearn) call can process an unbounded number of items if recursion chains. Combined with maxLearnDepth, this is bounded in theory (see section 5). | Low | Acceptable, but add a hard iteration cap as a safety net. |

### 4. Limit Mechanism (machine.go, cmd_elmb.go)

| # | Finding | Severity | Recommendation |
|---|---------|----------|----------------|
| 4.1 | **`--limit learn` correctly caps at Learn.** parseLimit("learn") returns ModeLearn (1). In arise(), `from >= m.Limit` is checked: when Learn (1) arises, `from` is 1 and `m.Limit` is 1, so `1 >= 1` is true, and the item goes to frame instead of Model. This is correct. | Informational | None needed. |
| 4.2 | **`--limit enact` prevents all learning.** With limit=ModeEnact (0), processEnact arises with from=0, and `0 >= 0` deposits directly to frame. Enact runs the command but the output is only framed, never analyzed. This is correct but may surprise users expecting at least the command to run. | Low | Document this behavior. |
| 4.3 | **parseLimit returns ModeBuild for unrecognized strings.** If a user passes `--limit foo`, they silently get full-depth processing. | Low | Log a warning or return an error for unrecognized limit values. |

### 5. Recursive Learn and Unbounded Stack Growth (learn.go)

| # | Finding | Severity | Recommendation |
|---|---------|----------|----------------|
| 5.1 | **Depth limit bounds recursion depth but not breadth.** maxLearnDepth=3 limits how deep RECURSE chains go, but each Learn call can produce multiple RECURSE items. With branching factor B, the worst case is B^3 Learn items. If the LLM consistently returns, say, 5 RECURSE directives, that is 125 Learn items at depth 3 alone, each spawning 2 infer calls (250 API calls just for Learn). | High | Add a breadth limit: cap the number of RECURSE directives accepted per processLearn invocation (e.g., max 3). Or add a global Learn budget counter. |
| 5.2 | **compactFrame is called during Learn iteration.** compactFrame calls inferDirect, which runs a synchronous subprocess. During this time, the machine is blocked. If compactFrame itself produces malformed output, the frame could be corrupted (indices shift). The reverse-order application of `=` directives in compactFrame is a good mitigation, but if the LLM produces overlapping ranges, the frame will be corrupted. | Medium | Validate that compact ranges do not overlap before applying them. Sort by Low descending and reject any range whose Low is less than the previous range's High. |
| 5.3 | **RECURSE items lack the original Command/Args.** When Learn pushes a RECURSE item, it sets Content and Depth but not Command or Args. If this item somehow reaches Enact (via a future relax path), processEnact will try to run an empty command. | Low | Carry forward the originating command context in RECURSE items, or ensure RECURSE items can never reach Enact. |

### 6. processModel Early Returns on DONE/INVESTIGATE (model.go)

| # | Finding | Severity | Recommendation |
|---|---------|----------|----------------|
| 6.1 | **DONE and INVESTIGATE cause immediate return, dropping subsequent directives.** If the LLM outputs `STEP: do X`, then `DONE`, the STEP is processed but the function returns on DONE before any arise. If the LLM outputs `INVESTIGATE: check Y`, then `STEP: do Z`, the STEP is silently dropped. This is by design (DONE means "stop", INVESTIGATE means "need more info"), but the STEP directives processed before the early return have already mutated the frame. | Medium | Either process all directives first and then decide the final action, or do not apply frame mutations (framePush for PLAN/STEP) until after the full directive list is scanned and the action is determined. Two-pass approach: parse directives, decide action, then apply. |
| 6.2 | **DONE arises the original item, not the plan.** When DONE is encountered, `m.arise(ModeModel, item)` arises the original input item, not any PLAN/STEP content that may have been accumulated. If PLAN/STEP directives appeared before DONE, they were added to the frame but the arisen item does not reflect them. | Medium | On DONE after partial plan accumulation, either arise the accumulated plan or do not arise at all (since "no action needed" was the determination). |

### 7. processBuild Re-queuing of Failures (build.go)

| # | Finding | Severity | Recommendation |
|---|---------|----------|----------------|
| 7.1 | **Re-queued failure items contain unparseable content.** When failures occur, the re-queued item's Content is: `"Previous failures:\n" + failures + "\n\nOriginal plan:\n" + item.Content`. parseBuildSteps only matches lines starting with `"STEP: "`. The failure context lines do not match this pattern. The "Original plan" section preserves the original STEP lines, so those will be re-parsed. However, the failure context is invisible to the retry -- the build retry has no knowledge of what failed or why. | High | The re-queued item should be sent through an LLM call that can read the failure context and produce revised STEP directives. Alternatively, restructure the retry to pass the failure context as a prompt to infer, generating a new plan that accounts for the failures. |
| 7.2 | **All original steps are retried, not just failures.** The "Original plan" in the re-queued content contains all original STEP lines, including ones that succeeded. This means successful steps are re-executed on retry. | High | Track which steps succeeded and exclude them from the re-queued content, or only re-queue the failed step texts as STEP lines. |
| 7.3 | **Build spawns child elmb with `--limit model`.** This means the child process runs Enact -> Learn -> Model but not Build. The child's Model output goes to the child's frame, which is captured as the result. This is a reasonable design choice but means the child cannot itself build -- it can only plan. If the step requires actual building, the child cannot do it. | Medium | Consider whether certain build steps should spawn children with `--limit build` to allow full recursive building. |

### 8. Frame Mutation During Iteration (machine.go, learn.go, build.go)

| # | Finding | Severity | Recommendation |
|---|---------|----------|----------------|
| 8.1 | **No concurrency races -- single-threaded processing.** The machine runs in a single goroutine (drain is synchronous). spawnAll/spawnAsync use goroutines but only for child processes; they do not mutate the machine's state concurrently. The WaitGroup synchronization in spawnAll is correct. No race conditions exist. | Informational | None needed. |
| 8.2 | **Frame mutation during compactFrame iteration.** compactFrame reads `m.Frames[name]` at the start but then applies directives that mutate `m.Frames[name]` in a loop. Each iteration creates a new slice, so the loop variable `directives` is stable. However, if two `=` directives reference overlapping ranges, the second directive's indices are stale because the first directive changed the frame length. The reverse-order application helps (higher indices first) but does not fully protect against overlapping ranges. | Medium | Validate non-overlapping ranges before applying. After each application, adjust remaining directive indices, or reject overlapping ranges entirely. |
| 8.3 | **frameRemoveRange and frameReplaceRange share a slice aliasing issue.** `append(f[:low], f[high:]...)` mutates the original backing array. If any other reference to the frame slice exists, it will see corrupted data. Currently no such aliasing occurs (frameClone makes a copy), but this is fragile. | Low | Use explicit new-slice construction (as compactFrame does) instead of in-place append for frame mutations. |

### 9. Full Adaptive Loop Completion (Cross-cutting)

| # | Finding | Severity | Recommendation |
|---|---------|----------|----------------|
| 9.1 | **The full loop cannot complete as described.** The intended scenario is: "enact tries X, learns X is not supported, models alternative, builds new tool, re-enacts with new tool, learns to verify." Tracing through the code: (1) Enact runs command, gets error -- but processEnact returns on error without arising, so Learn never sees it. Even if the command succeeds with error output in stdout, (2) Learn analyzes and arises to Model, (3) Model plans and arises to Build, (4) Build executes steps via child processes, adds results to frame, and terminates. There is no step (5) or (6) -- no re-enact and no verification learn. The loop is open, not closed. | Critical | To close the loop: (a) processEnact must arise error output to Learn, (b) processBuild must be able to relax to Enact or push re-enact items, (c) a verification mechanism must exist that pushes Build results back through Learn. |
| 9.2 | **spawnAny returns first result, ignores rest.** spawnAny reads one result from the channel and returns, calling cancel(). The remaining goroutines' context is cancelled but their results (including errors) are discarded. If the first result is an error, spawnAny returns that error even though other goroutines may have succeeded. | Medium | Collect results until one succeeds, or return the first success (not the first completion). |
| 9.3 | **infer/cmd_infer.go always calls BlockEnd even when no BlockStart occurred.** If the API returns 0 content blocks (empty response), `first` remains true, stopProgress is called, but BlockEnd is still emitted. This produces a malformed output block (`[exoutput]` without preceding `[  output]`) that will confuse parseOutputBlock in the parent process. | Medium | Guard BlockEnd with `if !first`. |

## Overall Assessment

**Grade**: C+ (Partial Implementation -- Sound Architecture, Incomplete Semantics)

The state machine architecture is well-conceived. The four-mode stack-based design with arise/relax transitions is a clean model for adaptive processing. The convention-based build system, output protocol, and child spawning infrastructure are solid engineering. The single-threaded processing model avoids concurrency hazards, and the frame abstraction provides a reasonable working memory.

However, the implementation falls short of the stated design intent in fundamental ways. The two critical findings -- (1) processEnact swallowing errors instead of arising them for learning, and (2) no re-enact path from Build back to Enact -- mean the core adaptive loop is open-ended rather than closed. The system can progress forward (E->L->M->B) but cannot loop back. This is not a state machine that learns from failure; it is a pipeline that processes success. The Build phase's re-queue mechanism is structurally broken due to the parseBuildSteps mismatch, and processModel's early returns can silently drop plan steps that have already mutated the frame. These issues compound: even if the loop were closed, the intermediate processing has reliability problems that would make the adaptive behavior unpredictable.

## Recommendations Summary

1. **[Critical] Close the adaptive loop.** Implement a re-enact mechanism in processBuild that can push verified commands back onto Stacks[ModeEnact]. Without this, the "build new tool, re-enact" scenario is impossible.

2. **[Critical] Arise errors from processEnact.** When a command fails, the error output is the most valuable learning signal. Arise it to Learn instead of silently discarding it.

3. **[High] Fix Build re-queue content mismatch.** Re-queued failure items must produce content that parseBuildSteps can parse. Either re-generate STEP directives via an LLM call that incorporates failure context, or restructure the re-queue to only include failed STEP lines.

4. **[High] Prevent re-execution of successful Build steps.** Track and exclude already-completed steps from retry content.

5. **[High] Add breadth limits to Learn recursion.** Cap the number of RECURSE directives per invocation to prevent exponential API call growth.

6. **[High] Fix LIFO ordering for Build steps across multiple Model outputs.** Multiple Build items from different Model calls will be processed in reverse arrival order, which may violate sequential dependencies.

7. **[Medium] Use two-pass directive processing in processModel.** Parse all directives first, determine the action (DONE/INVESTIGATE/PLAN), then apply frame mutations. This prevents partial frame mutation before early returns.

8. **[Medium] Validate compact ranges for overlap in compactFrame.** Reject or adjust overlapping `=` directives to prevent frame corruption.

9. **[Medium] Guard BlockEnd in infer/cmd_infer.go.** Only emit `[exoutput]` if `[  output]` was emitted, to prevent malformed output blocks.

10. **[Medium] Break drain() on relax.** When a relax operation occurs, consider breaking out of drain so the lower-priority mode can process immediately, giving relax true priority semantics.

11. **[Low] Warn on unrecognized `--limit` values.** parseLimit silently defaults to ModeBuild for unknown strings.

12. **[Low] Use explicit new-slice construction in frameRemoveRange/frameReplaceRange.** Avoid in-place append mutations that could cause slice aliasing bugs if the code evolves.
