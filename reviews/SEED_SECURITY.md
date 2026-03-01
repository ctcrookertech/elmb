# ELMB Seed — Security & Trust Boundaries Review

**Expert**: Sarah Kim, Process Security & Trust Boundaries (20 years experience)
**Scope**: seed/machine.go, seed/cmd_elmb.go, seed/learn.go, seed/model.go, seed/build.go, core/out.go, infer/cmd_infer.go
**Date**: 2026-02-28

## Executive Summary

The ELMB state machine has several significant security deficiencies centered around secrets exposure, insufficient trust boundaries between LLM-generated content and system operations, and an output protocol that is trivially spoofable by adversarial content. The most critical issues are the API key being passed as a process argument (visible to all users on the system) and the lack of any sanitization boundary between LLM output and subsequent system operations. These issues are systemic rather than incidental — the architecture treats the LLM as a trusted component, but it is in fact an untrusted input source.

## Findings

### 1. API Key Exposure via Process Arguments

| # | Finding | Severity | Recommendation |
|---|---------|----------|----------------|
| 1.1 | **API key passed as CLI argument to infer binary.** In `machine.go:265`, `inferDirect` passes `m.APIKey` as `args[0]` to the `infer` subprocess: `[]string{m.APIKey, "-"}`. The key is visible in `ps aux`, `/proc/PID/cmdline`, process accounting logs (`pacct`/`psacct`), crash dumps, and audit subsystems. Any user on the system can read it. | **Critical** | Pass the API key via environment variable (e.g., `cmd.Env = append(os.Environ(), "ELMB_API_KEY="+m.APIKey)`) or via stdin as a separate initial line before the prompt content. The `infer` binary should read the key from `os.Getenv()` rather than `os.Args[1]`. |
| 1.2 | **API key also passed in SpawnSpec Args in learn.go and build.go.** `learn.go:97-98` constructs `SpawnSpec{..., Args: []string{m.APIKey, "-"}, ...}` and `build.go:73` does the same. These spawn child `elmb` processes via `spawnSync`, which passes the args to the `elmb` binary's CLI. These child processes then invoke `infer` with the same arg-based key pattern, multiplying the exposure window. | **Critical** | Same remediation as 1.1. The SpawnSpec should not carry the API key in Args. Instead, child processes should inherit the key via environment. |
| 1.3 | **infer binary reads key from os.Args[1] and sets it in an HTTP header.** In `cmd_infer.go:21`, `key := os.Args[1]`. The key string lives in the process's argument list for the full duration of the HTTP request (including streaming, which can be long-lived). | **Critical** | Read from environment variable. Remove the key from the argument vector entirely. |

### 2. API Key File Handling

| # | Finding | Severity | Recommendation |
|---|---------|----------|----------------|
| 2.1 | **No permission check on anthropic.key.** `cmd_elmb.go:14` calls `os.ReadFile("anthropic.key")` with no check on file permissions. A world-readable key file (mode 0644 or 0666) in a shared directory will be silently consumed. | **High** | Before reading, call `os.Stat("anthropic.key")` and verify the file mode is no more permissive than 0600. Reject with a clear error if group or world bits are set. |
| 2.2 | **Key file read from working directory — CWD-dependent secret loading.** `resolveAPIKey()` reads from the relative path `"anthropic.key"`, meaning the key used depends entirely on which directory `elmb` is invoked from. An attacker who controls the CWD (e.g., via a cloned repository with a planted `anthropic.key`) can silently redirect API calls through their own key, enabling billing fraud or traffic interception via a proxy key. | **High** | Resolve the key file from a fixed, well-known location (e.g., `$HOME/.config/elmb/anthropic.key` or `$XDG_CONFIG_HOME/elmb/anthropic.key`). If a CWD-relative key is needed for development, require an explicit opt-in flag (e.g., `--key-file ./anthropic.key`) so it cannot happen silently. |
| 2.3 | **No warning when API key is empty.** If neither `ELMB_API_KEY` nor the key file provides a key, `resolveAPIKey()` returns `""` silently. The machine then runs with an empty key, and individual mode processors (learn, model) silently skip LLM calls via passthrough. No warning is ever emitted to the user that the system is operating in a degraded mode. | **Low** | Emit a `core.Line(core.Error, ...)` warning at startup if the API key is empty, so the user knows LLM-dependent modes will be skipped. |

### 3. Command Injection via siblingPath

| # | Finding | Severity | Recommendation |
|---|---------|----------|----------------|
| 3.1 | **siblingPath does not sanitize the `name` parameter.** `machine.go:420-427`: `siblingPath(name)` calls `filepath.Join(filepath.Dir(exe), name)`. If `name` contains path traversal sequences like `../../../bin/sh`, `filepath.Join` will resolve them, and the resulting path will point outside the sibling directory. The `name` comes from `runCommand(command, args)` where `command` originates from `Item.Command`, and in `processEnact` the item's Command field comes from user input (`args[0]` in `cmd_elmb.go:40`). | **High** | Validate that `name` contains no path separators and no `.` prefixes: `if strings.ContainsAny(name, "/\\") || strings.HasPrefix(name, ".") { reject }`. Alternatively, after `filepath.Join`, verify the result is still within `filepath.Dir(exe)` using `filepath.Rel` or prefix checking. |
| 3.2 | **LLM-generated SpawnSpec.Command is not validated.** In `build.go:70-74`, `SpawnSpec{Command: "infer", ...}` is hardcoded, and in `spawnSync`/`spawnAll`/`spawnAny`/`spawnAsync` the binary is always `siblingPath("elmb")`. However, the architecture allows `SpawnSpec.Command` to carry arbitrary strings that become CLI arguments to the child `elmb` process. If a future code path sets `Command` from LLM output, this becomes an injection vector through `siblingPath`. | **Medium** | Maintain an allowlist of valid command names (e.g., `{"elmb", "infer"}`) and validate against it before calling `siblingPath`. |

### 4. LLM-Driven Command Execution and Indirect Injection

| # | Finding | Severity | Recommendation |
|---|---------|----------|----------------|
| 4.1 | **Build mode spawns processes based on LLM-classified "actionable" steps.** `build.go:31-39` uses a keyword heuristic (`looksActionable`) to decide whether to spawn a child process for a step. The step text itself comes from LLM output parsed in `model.go`. An adversarial LLM response could craft steps that match the heuristic, causing child processes to be spawned with attacker-controlled content passed via stdin. | **High** | This is an indirect prompt injection surface. The `looksActionable` heuristic provides no real security — it is a classification function, not a security boundary. Consider: (a) requiring user confirmation before spawning child processes, (b) implementing a capability-based permission model where the user pre-approves which operations the build mode can perform, or (c) adding a step count limit per invocation. |
| 4.2 | **No depth or breadth limits on learn recursion beyond maxLearnDepth.** `learn.go:126` checks `item.Depth < maxLearnDepth` (3), but within each depth level there is no limit on how many RECURSE directives the LLM can emit. A malicious LLM response could emit hundreds of RECURSE directives, causing an explosion of child infer processes and API calls. | **Medium** | Add a per-level recursion breadth limit (e.g., max 5 RECURSE directives per learn invocation). |
| 4.3 | **Build retry loop is LLM-influenced.** `build.go:86-94` re-queues failed steps at depth+1 with the error context included in the content. The error messages from failed subprocesses are fed back to the LLM in subsequent iterations. An attacker who controls a subprocess's error output could inject content that influences the LLM's retry behavior. | **Medium** | Sanitize error messages before including them in re-queued content. Limit the size of error context passed back. |

### 5. Stdin Piping of LLM Content

| # | Finding | Severity | Recommendation |
|---|---------|----------|----------------|
| 5.1 | **LLM-generated content is piped verbatim via stdin to child processes.** `machine.go:253-254` and the various spawn functions pipe `SpawnSpec.Stdin` (which contains LLM-generated prompts and content) directly to child process stdin. If a child process interprets stdin content as commands (e.g., a future command that processes stdin line-by-line as directives), LLM-generated content could trigger unintended operations. | **Medium** | Currently, the only stdin consumers are `infer` (which treats it as prompt text) and `elmb` (which does not read stdin). The risk is low today but grows as new commands are added. Establish a convention that stdin content is always treated as opaque data, never as a command stream. Document this as a security invariant. |

### 6. Frame Content Injection (Prompt Injection Persistence)

| # | Finding | Severity | Recommendation |
|---|---------|----------|----------------|
| 6.1 | **Frame elements from LLM output are included verbatim in subsequent prompts.** In `learn.go:116`, LLM-generated text from `+` directives is added to the frame via `framePush`. In `learn.go:78` and `model.go:49`, `frameText("")` serializes all frame elements into the prompt for the next LLM call. An LLM could inject prompt manipulation content (e.g., "Ignore all previous instructions and...") that persists in the frame and corrupts all future infer calls. | **High** | This is a persistent prompt injection vector. Mitigations: (a) Tag frame elements with their origin (LLM vs. system) and present them differently in prompts (e.g., within XML-like delimiters that the system prompt instructs the LLM to treat as data, not instructions). (b) Limit frame element size. (c) Consider a frame element validation step that rejects content matching known prompt injection patterns. |
| 6.2 | **compactFrame feeds the entire frame back to the LLM for summarization.** `learn.go:176-180` sends all frame entries to the LLM and trusts the LLM's `=` directives to replace frame ranges. A compromised or adversarial LLM response could replace legitimate frame entries with injected content, effectively rewriting the machine's memory. | **High** | The compaction operation grants the LLM write access to the machine's persistent state. Consider: (a) preserving original entries alongside compacted versions so compaction is reversible, (b) limiting the scope of `=` directives (e.g., only allowing adjacent entries to be merged, not arbitrary range replacement). |

### 7. Output Protocol Injection

| # | Finding | Severity | Recommendation |
|---|---------|----------|----------------|
| 7.1 | **parseOutputBlock is spoofable by LLM output containing protocol markers.** `machine.go:429-461` parses output by looking for lines matching `[  output]` and `[exoutput]`. If an LLM response (streamed via `infer`) contains these literal strings, the `infer` binary will emit them as part of the block content (`core.Print(event.Delta.Text)` in `cmd_infer.go:94`), and `parseOutputBlock` will misparse the boundary. Specifically: a premature `[exoutput]` in the LLM response will truncate the captured output. A fake `[  output]` followed by injected content and `[exoutput]` could replace the real output entirely. | **High** | Escape or encode protocol markers in LLM output before emitting them. For example, in `cmd_infer.go`, replace any occurrence of `[  output]` or `[exoutput]` in `event.Delta.Text` with an escaped form before calling `core.Print`. Alternatively, use a delimiter that cannot appear in UTF-8 text (e.g., a NUL-delimited binary protocol). |
| 7.2 | **ANSI stripping in parseOutputBlock is incomplete.** `machine.go:431-443` strips `\033[...m` sequences, but only handles the `m` terminator. ANSI escapes can use other terminators (`H`, `J`, `K`, `A`-`D`, etc.). A crafted response could use non-`m` ANSI sequences to inject control characters that survive the stripping pass. | **Low** | Use a more robust ANSI stripping approach that handles all CSI sequences (terminators in the range 0x40-0x7E). |

### 8. Working Directory Trust

| # | Finding | Severity | Recommendation |
|---|---------|----------|----------------|
| 8.1 | **CWD-dependent key loading enables silent key substitution.** (Covered in 2.2, elaborated here for completeness.) If `elmb` is invoked from a directory containing a malicious `anthropic.key`, the attacker's key is used. This is particularly dangerous in CI/CD pipelines where the working directory is a cloned repository. An attacker could submit a PR that includes an `anthropic.key` file, and if the CI system runs `elmb` from the repo root, the attacker's key is used — potentially routing API traffic through an attacker-controlled proxy. | **High** | See recommendation in 2.2. Additionally, consider logging which key source was used (env var vs. file) and the file path, so users can audit key provenance. |

### 9. TLS Verification

| # | Finding | Severity | Recommendation |
|---|---------|----------|----------------|
| 9.1 | **http.DefaultClient uses system certificate store — acceptable for production use.** `cmd_infer.go:49` uses `http.DefaultClient.Do(req)`, which uses the system's TLS certificate store. This is the standard and correct behavior for Go applications. The system cert store is managed by the OS and includes only trusted CAs. | **Info** | This is acceptable. Go's `crypto/tls` defaults are secure (TLS 1.2+, strong cipher suites). No action needed unless the deployment environment requires certificate pinning for the Anthropic API endpoint, which is not typical. |
| 9.2 | **No request timeout on HTTP client.** `http.DefaultClient` has no timeout configured. If the Anthropic API hangs or a network issue causes a stall, the `infer` process will block indefinitely. While not a direct security vulnerability, it can be used for resource exhaustion (an attacker who can cause DNS poisoning or network interception could hold connections open). | **Low** | Set an explicit timeout: `client := &http.Client{Timeout: 120 * time.Second}`. |

### 10. Additional Observations

| # | Finding | Severity | Recommendation |
|---|---------|----------|----------------|
| 10.1 | **No rate limiting on API calls.** The learn and build modes can spawn many concurrent infer calls (e.g., `spawnAll` in learn.go launches parallel processes, build.go iterates over all steps sequentially but with retries). There is no rate limiting, cost cap, or call budget. A runaway loop or adversarial LLM response could generate unbounded API costs. | **Medium** | Implement a call budget (e.g., max N infer calls per machine invocation) with a clear error when exhausted. |
| 10.2 | **json.Marshal error silently ignored in cmd_infer.go.** `cmd_infer.go:35`: `body, _ := json.Marshal(...)`. While this particular marshal call is unlikely to fail (it's marshaling basic Go types), silently ignoring errors is a bad habit that can mask issues in future modifications. | **Low** | Check the error: `body, err := json.Marshal(...); if err != nil { core.Errorf(...); os.Exit(1) }`. |
| 10.3 | **http.NewRequest error silently ignored in cmd_infer.go.** `cmd_infer.go:42`: `req, _ := http.NewRequest(...)`. Same pattern as 10.2. | **Low** | Check the error. |
| 10.4 | **Child processes inherit full parent environment.** `spawnSync`, `spawnAll`, `spawnAny`, and `spawnAsync` do not set `cmd.Env`, so child processes inherit the parent's full environment including any sensitive variables. This is standard Go behavior and currently benign, but it means any secrets in the environment (database credentials, cloud provider keys, etc.) are accessible to child processes and, by extension, to any code those processes execute. | **Low** | If the environment is known to contain sensitive variables beyond `ELMB_API_KEY`, consider filtering the environment for child processes to only include necessary variables. |

## Overall Assessment

**Grade: C-** (Significant security deficiencies requiring remediation before production use)

The codebase demonstrates a clear, well-structured architecture with a disciplined approach to output formatting and state management. However, it has fundamental security shortcomings that would be unacceptable in any system that handles API keys or processes untrusted input from an LLM.

The most urgent issue is the API key exposure via process arguments (findings 1.1-1.3). This is a well-known antipattern that exposes secrets to every user and process on the system. The fix is straightforward (environment variables) and should be applied immediately. The second critical area is the absence of any trust boundary between LLM-generated content and system operations (findings 4.1, 6.1, 6.2, 7.1). The system treats LLM output as trusted data, but LLMs are susceptible to prompt injection and can produce adversarial content. The output protocol is trivially spoofable, frame content is a persistent injection vector, and the build mode spawns processes based on LLM classification. These issues require architectural attention — they cannot be fixed with simple input validation. The key file handling issues (findings 2.1, 2.2, 8.1) are high severity in shared or CI/CD environments where the working directory is not controlled by the user.

## Recommendations Summary

1. **[Critical] Eliminate API key from process arguments.** Switch to environment variable passing for all subprocess invocations (infer, child elmb). Remove the key from os.Args entirely. This is the single highest-impact fix.
2. **[High] Add output protocol escaping.** Escape or encode `[  output]` and `[exoutput]` markers in LLM-streamed content within `cmd_infer.go` to prevent protocol injection.
3. **[High] Validate siblingPath input.** Reject command names containing path separators or dot prefixes. Maintain an allowlist of valid sibling binaries.
4. **[High] Fix key file resolution.** Read from a fixed config directory (e.g., `$HOME/.config/elmb/`), check file permissions (reject if group/world readable), and log which key source was used.
5. **[High] Tag frame element provenance.** Distinguish LLM-generated frame content from system-generated content. Present LLM-originated content within clear delimiters in prompts to reduce prompt injection persistence.
6. **[High] Gate build-mode process spawning.** Add user confirmation or a capability allowlist before spawning child processes from LLM-classified actionable steps.
7. **[Medium] Add API call budget.** Implement a per-invocation limit on infer calls to prevent runaway costs from adversarial LLM responses or recursive loops.
8. **[Medium] Add breadth limit on learn recursion.** Cap the number of RECURSE directives processed per learn invocation.
9. **[Medium] Sanitize error context in build retries.** Limit size and sanitize subprocess error output before feeding it back into LLM prompts.
10. **[Low] Add HTTP client timeout.** Configure an explicit timeout on the HTTP client in `cmd_infer.go`.
11. **[Low] Handle ignored errors in cmd_infer.go.** Check return values from `json.Marshal` and `http.NewRequest`.
12. **[Low] Warn on empty API key at startup.** Emit a visible warning so users know LLM-dependent modes are being silently skipped.
