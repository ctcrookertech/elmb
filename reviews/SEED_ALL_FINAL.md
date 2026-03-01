# ELMB Seed — Final Expert Review

**Synthesized by**: Dr. Raj Mehta, Software Systems Architecture (28 years experience)
**Date**: 2026-02-28
**Expert reports reviewed**: 5

## Executive Summary

The ELMB state machine implements an ambitious four-mode adaptive loop (Enact, Learn, Model, Build) with clean architectural abstractions — arise/relax transitions, frame-based context accumulation, and process-based isolation. The forward pipeline (E->L->M->B) works correctly for simple cases. However, five expert reviews converge on three blocking issues that prevent the system from achieving its stated design goal: (1) the adaptive loop is open — Build cannot push work back to Enact, and Enact silently drops errors instead of arising them for learning; (2) there are no termination guarantees — the Learn-Model cycle can loop infinitely, API calls are unbounded, and no timeouts exist anywhere; (3) the LLM integration lacks the guardrails needed for reliable autonomous operation — no system prompts, fragile directive parsing, and an output protocol that LLM content can spoof. The architecture is sound; the implementation needs safety mechanisms before it can run unattended.

## Expert Reports Summary

| Expert | Specialty | Grade | Key Concern |
|--------|-----------|-------|-------------|
| Dr. Helena Voss | State Machine & Automata | C+ | Adaptive loop is open — no re-enact path from Build, errors silently dropped |
| Marcus Chen | Concurrency & Deadlocks | C+ | No timeouts anywhere, stdout corruption from concurrent progress, process orphaning |
| Dr. Anya Petrov | LLM Orchestration | C- | No system prompts, broken adaptive loop, output protocol spoofable by LLM content |
| James Okoro | Reliability & Fault Tolerance | D+ | Infinite Learn-Model cycle, no API budget, no crash recovery, exponential RECURSE branching |
| Sarah Kim | Process Security | C- | API key in process args, persistent prompt injection via frame, CWD-dependent key loading |

## Cross-Cutting Themes

### 1. The Adaptive Loop Is Open (All 5 experts)

Every expert identified that the system's core value proposition — "enact, learn from failure, model alternatives, build tools, re-enact" — cannot complete. Two specific breaks:
- **processEnact drops errors** (machine.go:178-180): When a command fails, the error is logged and the item vanishes. The failure signal never reaches Learn, so the system cannot learn from failure.
- **processBuild has no re-enact path** (build.go): Build adds results to the frame and terminates. It never pushes work to ModeEnact. The loop flows strictly forward.

### 2. No Termination Guarantees (State Machine, Reliability, Concurrency)

Three independent mechanisms can produce unbounded execution:
- **Learn-Model infinite cycle**: Model's INVESTIGATE relaxes to Learn with `Depth: 0`, bypassing maxLearnDepth. If the LLM consistently outputs INVESTIGATE, this loops forever.
- **Exponential RECURSE branching**: maxLearnDepth=3 limits depth but not breadth. With branching factor R, worst case is R^3 Learn items, each making 2+ API calls.
- **No timeouts or budgets**: All spawn functions, HTTP calls, and child processes block indefinitely. No global API call cap exists.

### 3. LLM Output Is Treated as Trusted (LLM Orchestration, Security)

The system sends prompts as user messages with no system prompt, making directive compliance unreliable. All three parsers silently skip non-matching lines. The output protocol delimiters (`[  output]`/`[exoutput]`) can appear in LLM responses, corrupting parsing. Frame elements from LLM output persist and are injected verbatim into future prompts, creating a persistent prompt injection surface.

### 4. API Key Exposure (Security, LLM Orchestration)

The API key is passed as a CLI argument to the infer binary, visible via `ps`, `/proc/PID/cmdline`, and audit logs. The key file is read from CWD with no permission check. Child elmb processes resolve their own key via environment/file, which may silently fail.

### 5. Process Lifecycle Gaps (Concurrency, Reliability)

No process groups mean killed parents orphan children. Context cancellation sends SIGKILL (not SIGTERM), preventing child cleanup. Concurrent progress indicators corrupt the stdout output protocol that parent processes rely on for parsing.

## Recommendations

### Accepted

| # | Recommendation | Source Expert(s) | Priority | Rationale |
|---|---------------|-----------------|----------|-----------|
| 1 | **Close the adaptive loop**: processEnact must arise errors to Learn; processBuild must be able to push re-enact items to ModeEnact (e.g., via `REENACT:` directive) | Voss, Petrov, Okoro | Critical | Without this, the system's core design goal is unachievable. This is the single most important functional fix. |
| 2 | **Break the Learn-Model infinite cycle**: Track relax transitions on Item (add RelaxCount or CycleCount), cap at 3-5, force DONE when reached | Okoro, Voss | Critical | An unattended system with no termination guarantee will eventually hang. The Depth: 0 reset on relax makes maxLearnDepth ineffective for this case. |
| 3 | **Add a global API call budget**: Configurable cap (default 50), decrement on every inferDirect and spawnSync, gracefully finalize when exhausted | Okoro, Chen, Kim | Critical | Caps the blast radius of every other unbounded-execution issue simultaneously. Highest leverage single safety mechanism. |
| 4 | **Add timeouts to all external calls**: `exec.CommandContext` with deadline for child processes, `http.Client{Timeout}` in infer | Chen, Okoro | Critical | A single hung API call or child process currently freezes the entire process tree forever. |
| 5 | **Move API key to environment variable**: infer reads from `ELMB_API_KEY` env instead of os.Args; parent sets `cmd.Env` for children | Kim, Petrov | High | Process arguments are visible system-wide. Straightforward fix with high security impact. |
| 6 | **Add system prompts to infer calls**: Add `"system"` field to the API request body with format-constraining instructions | Petrov | High | Single highest-leverage change for LLM output reliability. Moves format instructions from user message (weakest compliance position) to system prompt (strongest). |
| 7 | **Cap RECURSE breadth in Learn**: Add maxRecursePerItem (3-5) to prevent LLM-driven exponential branching | Voss, Chen, Okoro, Kim | High | Without this, a single LLM response with many RECURSE directives can spawn hundreds of API calls. |
| 8 | **Fix Build re-queue to only re-queue failed steps**: Track successes, exclude them from retry content; or send failure context through an LLM call to produce revised steps | Voss | High | Current implementation re-executes succeeded steps and produces content that parseBuildSteps can't parse. |
| 9 | **Serialize progress indicator output**: Single progress indicator for duration of spawnAll, or mutex around all stdout writes | Chen, Okoro | High | Concurrent progress output corrupts the structured output protocol, causing parseOutputBlock to silently return wrong results in parent processes. |
| 10 | **Escape output protocol markers in LLM content**: In cmd_infer.go, escape `[  output]` and `[exoutput]` in streamed text before calling core.Print | Petrov, Kim | High | LLM responses containing these strings will truncate or corrupt captured output. |
| 11 | **Validate siblingPath input**: Reject names with path separators; maintain allowlist of valid sibling binaries | Kim | High | Prevents path traversal from user-supplied command names. |
| 12 | **Fix key file resolution**: Read from fixed config dir (~/.config/elmb/), check file permissions (reject >0600), log key source | Kim | High | CWD-dependent key loading is exploitable in CI/CD and shared environments. |
| 13 | **Fix Model-to-Learn relaxation semantics**: Learn must detect Source: Model and use investigation-specific prompts, or route INVESTIGATE through Enact | Petrov, Voss | High | Currently sends a question to a prompt expecting command output, producing confused LLM responses. |
| 14 | **Capture child stderr on failure**: Set cmd.Stderr to buffer, include in error messages | Okoro, Chen | Medium | Currently "exit status 1" with no diagnostic info. Single highest-leverage debugging improvement. |
| 15 | **Validate compaction ranges for overlap**: Sort by Low descending, reject overlapping ranges before applying | Voss, Petrov, Okoro | Medium | Overlapping ranges corrupt frame indices. Apply against snapshot of original frame. |
| 16 | **Add process group management**: Set Setpgid: true on child processes, kill process groups on cleanup | Chen, Okoro | Medium | Prevents orphaned grandchildren on parent kill or context cancellation. |
| 17 | **Propagate exit codes**: Track errors in Machine, exit non-zero on unresolved failures | Okoro | Medium | Parent processes and operators cannot currently distinguish success from failure. |

### Shelved

| # | Recommendation | Source Expert(s) | Reason Shelved |
|---|---------------|-----------------|----------------|
| 1 | Implement checkpointing/journaling for crash recovery | Okoro | Significant complexity for a system that doesn't yet have its core loop working. Revisit after the adaptive loop is closed and termination is guaranteed. |
| 2 | Use Anthropic tool-use API for structured output | Petrov | Good long-term direction, but adds API integration complexity. System prompts (accepted #6) address 80% of the parsing reliability problem at 10% of the cost. Revisit when directive parsing proves insufficient with system prompts. |
| 3 | Implement prompt-hash-to-response caching | Okoro | Premature optimization. The API budget (accepted #3) addresses the cost concern. Caching adds complexity around staleness and non-determinism. |
| 4 | Content-addressed store for Item.Content | Okoro | Memory optimization not needed until the system runs long enough to accumulate significant content, which requires termination guarantees first. |
| 5 | Filter frame entries by FrameLevel in prompts | Petrov | Good idea but requires understanding which levels are useful for which modes. Defer until the adaptive loop works and real usage patterns emerge. |
| 6 | Frame element provenance tagging for prompt injection defense | Kim | Important for production but requires architectural decisions about trust boundaries. Address after the functional loop works. |
| 7 | Gate build-mode process spawning with user confirmation | Kim | Would break autonomous operation. The API budget and depth limits provide sufficient containment for now. Revisit for production deployment. |
| 8 | Remove or guard spawnAsync (dead code) | Chen | Low priority. It's unused and harmless. Clean up in a future housekeeping pass. |
| 9 | Make drain() interruptible on relax for priority semantics | Voss | Behavioral change that could introduce new edge cases. Current drain-then-scan is predictable. Revisit if Learn-Model ordering proves problematic in practice. |

## Priority Action Plan

**Phase 1 — Safety (must-do before any unattended use):**
1. Add global API call budget with configurable cap (accepted #3)
2. Break Learn-Model infinite cycle with RelaxCount on Item (accepted #2)
3. Add timeouts: `exec.CommandContext` for children, `http.Client.Timeout` for infer (accepted #4)
4. Move API key from process args to environment variable (accepted #5)

**Phase 2 — Functional completeness (close the loop):**
5. processEnact: arise error output to Learn instead of silently dropping (accepted #1)
6. processBuild: add REENACT directive to push work back to Enact (accepted #1)
7. Fix Build re-queue to exclude succeeded steps and produce parseable content (accepted #8)
8. Fix Model-to-Learn relaxation with source-aware prompts (accepted #13)

**Phase 3 — Reliability hardening:**
9. Add system prompts to infer API calls (accepted #6)
10. Cap RECURSE breadth per Learn invocation (accepted #7)
11. Serialize progress output to prevent protocol corruption (accepted #9)
12. Escape output protocol markers in LLM content (accepted #10)
13. Validate siblingPath input and compaction ranges (accepted #11, #15)

**Phase 4 — Operational quality:**
14. Fix key file resolution to fixed config dir with permission check (accepted #12)
15. Capture child stderr, propagate exit codes (accepted #14, #17)
16. Add process group management (accepted #16)

## Overall Assessment

**Grade: C- (Needs Improvement)**

The ELMB state machine has a well-conceived architecture that solves a genuinely hard problem — recursive, adaptive task decomposition through LLM-driven control flow. The four-mode pipeline with arise/relax transitions is an elegant abstraction. The convention-based build system, output protocol, and process isolation model are clean engineering. The code is readable and well-structured.

However, the system is in a state where the architecture promises more than the implementation delivers. The adaptive loop — the system's core value proposition — cannot complete: errors are silently dropped, and Build has no path back to Enact. The system has no termination guarantee: the Learn-Model cycle can loop infinitely, API calls are unbounded, and nothing times out. The LLM integration lacks the safety mechanisms (system prompts, output escaping, parsing hardening) needed for the LLM to reliably play its role as a control-flow driver. The API key handling would fail any security review.

The good news is that none of these issues require architectural changes. The foundation is solid. The fixes are well-defined: add a budget counter, add a cycle counter, add timeouts, add system prompts, move the key to env vars, and wire up the re-enact path. Phase 1 (safety) and Phase 2 (loop closure) together represent perhaps 200-300 lines of focused changes that would transform this from a forward-only pipeline into the adaptive loop it was designed to be.
