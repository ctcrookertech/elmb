# SKYGOD Review: Project Hygiene Pass

**Date**: 2026-02-28
**Scope**: Full project (seed/, infer/, core/)
**Files Analyzed**: 11

## Summary

The codebase is structurally sound with clear mode separation and a consistent output convention. Two functional bugs undermine the pipeline (REENACT directives silently dropped by model, stdin not forwarded through child elmb to enact subprocesses). Significant dead code accumulated from the async/frame infrastructure that was built but never wired to callers. Subprocess setup boilerplate is repeated 6 times, crossing the threshold where inline repetition hurts maintainability more than a same-file helper would.

## Findings by Pillar

### S: SOLID

| # | File | Line | Issue | Severity | Fix |
|---|------|------|-------|----------|-----|
| 1 | seed/machine.go | 376-401 | `runCommandWithInput` is a superset of `runCommand` (identical logic plus optional stdin) — two functions with the same responsibility | Integral (3) | Delete `runCommand`, rename `runCommandWithInput` to `runCommand`, pass `""` for stdin at the one call site in `processEnact` |
| 2 | seed/model.go | 42-109 | `processModel` has two responsibilities: parsing LLM output AND assembling the plan content. When REENACT was added to the prompt, the parser silently discards it (see O&O #1) because the directive types weren't extended | Functional (1) | See O&O #1 fix |

### K: KISS

| # | File | Line | Issue | Severity | Fix |
|---|------|------|-------|----------|-----|
| 1 | seed/cmd_elmb.go | 38-42 | `goto doneFlags` to exit the flag parsing loop is unusual Go control flow | Integral (3) | Restructure as `flagsDone := false; for len(args) > 0 && !flagsDone { switch args[0] { ... default: flagsDone = true } }` |

### Y: YAGNI

| # | File | Line | Issue | Severity | Fix |
|---|------|------|-------|----------|-----|
| 1 | seed/machine.go | 267-301 | `framePop`, `frameRemoveRange`, `frameReplaceRange`, `frameClone`, `frameSwap`, `frameCreate` — zero callers in the codebase | Integral (3) | Delete all six functions. Re-add individually if/when a mode processor needs them |
| 2 | seed/machine.go | 376-401 | `runCommandWithInput` — zero callers (was the old infer path, now replaced by `inferWithSystem`) | Integral (3) | Delete. The only command+stdin path is `inferWithSystem` |
| 3 | seed/machine.go | 492-524 | `spawnAny` — zero callers | Integral (3) | Delete |
| 4 | seed/machine.go | 526-598 | `AsyncResult`, `AsyncHandle`, `spawnAsync`, `Cancel`, `AllDone`, `Results` — zero callers, ~70 lines of async infrastructure | Integral (3) | Delete entire async subsystem |
| 5 | seed/machine.go | 83 | `SpawnSpec.BaseFrame` field — never read or set anywhere | Integral (3) | Remove field from struct |

### G: GRASP

| # | File | Line | Issue | Severity | Fix |
|---|------|------|-------|----------|-----|
| 1 | seed/machine.go | 405-427 | `runCommand` is a near-exact duplicate of `runCommandWithInput` minus the stdin parameter — scattered responsibility for "run a sibling process" | Integral (3) | Consolidate into one function (see SOLID #1) |

### O: O&O

| # | File | Line | Issue | Severity | Fix |
|---|------|------|-------|----------|-----|
| 1 | seed/model.go | 58 | REENACT directive is offered in the LLM prompt but `parseModelDirectives` doesn't recognize it — REENACT lines are silently dropped with "skipping unrecognized line" and never reach Build's `parseBuildSteps` | Functional (1) | Add REENACT parsing to `parseModelDirectives` (as a pass-through that includes the raw `REENACT: ...` line in `planParts`), or restructure so the model's full response text (not just parsed directives) flows to Build |
| 2 | seed/machine.go | 405-427 | `runCommand` does not forward `os.Stdin` to the subprocess. When a child elmb is spawned (via `spawnSync`) with `Stdin` piped, `processEnact` calls `runCommand` for the seed command, but that subprocess gets DevNull. The infer `-` path reads nothing. | Functional (1) | Set `cmd.Stdin = os.Stdin` in `runCommand` so the child elmb forwards its piped stdin to the enact subprocess |
| 3 | seed/trace.go | 115 | `json.MarshalIndent` error discarded without comment | Integral (3) | Add `// infallible: marshaling known Go structs` |
| 4 | infer/cmd_infer.go | 70 | `json.Marshal(reqBody)` error discarded without comment | Integral (3) | Add `// infallible: marshaling code-constructed map` |
| 5 | infer/cmd_infer.go | 75 | `http.NewRequestWithContext` error discarded without comment | Integral (3) | Add `// infallible: constant URL and valid method` |
| 6 | infer/cmd_infer.go | 94 | `io.ReadAll(resp.Body)` error discarded without comment | Integral (3) | Add `// best-effort: logging error response body` |
| 7 | seed/config.go | 53-55 | `os.UserHomeDir()` failure returns empty string with no trace — silent operation per CLAUDE.md | Operational (2) | Add `TraceLine("config", "cannot determine home directory: "+err.Error())` |
| 8 | seed/config.go | 67-69 | `os.ReadFile(keyPath)` failure after successful stat returns empty string with no trace | Operational (2) | Add `TraceLine("config", "cannot read key file: "+err.Error())` |
| 9 | seed/trace.go | 150 | `fmt.Sscanf` error on SET_BUDGET silently accepts 0 on parse failure — LLM sends "SET_BUDGET: abc", budget becomes 0 | Operational (2) | Check Sscanf return count: `if n, _ := fmt.Sscanf(...); n != 1 { continue }` |

### D: DRY

| # | File | Line | Issue | Severity | Fix |
|---|------|------|-------|----------|-----|
| 1 | seed/machine.go | 334-427, 441-469, 492-524, 560-598 | Subprocess setup (siblingPath + CommandContext + Env + SysProcAttr + stderr Buffer + StartProgress/Newline + error formatting) repeated in `inferWithSystem`, `runCommand`/`runCommandWithInput`, `spawnSync`, `spawnAny`, `spawnAsync` — 6 copies of ~12 lines each | Integral (3) | Extract a same-file helper `func (m *Machine) execSibling(name string, args []string, stdin string) ([]byte, error)` that handles the common setup. Each caller just calls the helper and post-processes the output. This is within the same package/file — not a new package. |
| 2 | seed/trace.go | 22, 31, 33, 47, 57, 88, 91, 128, 130, 139, 144, 154, 160, 179, 188, 205, 207 | Debug/trace tag strings `"\033[90m[   trace]\033[0m"` and `"\033[90m[   debug]\033[0m"` repeated ~17 times | Integral (3) | Define `const traceTag = "\033[90m[   trace]\033[0m"` and `const debugTag = "\033[90m[   debug]\033[0m"`, use in all Fprintf calls |
| 3 | seed/trace.go | 127, 155-163, 164-182, 183-191 | Mode-name-to-Mode lookup loop `for mode := ModeEnact; mode < modeCount; mode++ { if modeNames[mode] == name { ... } }` repeated 3 times in `applyDebugDirectives` | Integral (3) | Extract `func modeByName(name string) (Mode, bool)` used by all three blocks |

## Priority Actions

1. **Fix REENACT directive drop** (O&O #1) — Model prompts the LLM for REENACT but parseModelDirectives discards it. No REENACT directive ever reaches Build. This is a functional gap in the adaptive loop.

2. **Fix stdin forwarding in runCommand** (O&O #2) — Child elmb processes spawned with piped stdin never deliver that stdin to the enact subprocess. `infer -` reads nothing. This breaks the learn spawn pattern where prompts are piped through child elmb to infer.

3. **Delete dead code** (YAGNI #1-5) — 6 frame functions, `runCommandWithInput`, `spawnAny`, entire async subsystem, `BaseFrame` field — ~120 lines with zero callers. Removing them reduces the maintenance surface and eliminates confusion about what's actually used.

4. **Consolidate subprocess setup** (DRY #1, SOLID #1, GRASP #1) — Extract `execSibling` helper within machine.go. Cuts ~60 lines of repetition and makes future subprocess changes (e.g. adding a new env var) a single-point fix.

5. **Add error discard comments** (O&O #3-6) — Four discarded errors need `// infallible` or `// best-effort` annotations per the error contract rules.

6. **Add trace lines for silent config failures** (O&O #7-8) — Two config resolution paths fail silently, violating the project's "silent operations are bugs" principle.
