# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Design Principles

**Minimize operations.** Every function, variable, and file should exist because it directly serves a functional goal. Prefer fewer moving parts over elegance. We don't care about code size — we care about the complexity-to-capability ratio. If three lines of straight code do the job, don't abstract them.

**Self-describing names.** Names are the documentation. A function, variable, or file name should make its purpose obvious without requiring a comment. If you need a comment to explain *what* something does, rename it instead.

**Trace every operation with context.** When an operation runs, it should say what it's doing. Build prints what it's building and from where. Clean says it's cleaning. Silent operations are bugs.

**No silent failures.** Nothing is "best effort". Every operation either succeeds or fails visibly. Do not swallow errors or hide failures behind fallback paths.

**No external dependencies in executables.** All executables use only the Go standard library. Build tooling (magefiles) may use external packages.

## Build System

Mage (zero-install) wrapped by npm scripts. Always use the npm wrappers — never run `go` or `mage` commands directly:

```
npm run build    # compile all targets to dist/
npm run vet      # go vet ./...
npm run lint     # staticcheck ./...
npm test         # go test ./...
npm run check    # vet + lint + test in sequence
npm run clean    # rm -rf dist/
npm run tidy     # go mod tidy
```

## Convention-Based Build

The build system auto-discovers executables by scanning top-level directories:

- A file named `cmd_<name>.go` in a directory marks it as a build target
- That directory is compiled and the binary is output to `dist/<name>` (`.exe` on Windows)
- Directories without a `cmd_*.go` file are skipped for building (different convention, manual setup)
- Well-known directories (`dist/`, `magefiles/`, `node_modules/`) are always skipped

Example: `seed/cmd_elmb.go` → `dist/elmb`

To add a new executable: create a new directory with a `cmd_<name>.go` containing `package main` and a `main()` function.

## Output Convention

All output from executables is prefixed with an 8-character label in brackets, right-aligned with space padding:

```
[progress] ....         # one dot every 200ms while waiting
[  output] single line  # inline: output is the rest of this line
[   error] message      # error output
```

Multi-line output: `[  output]` followed by a newline starts a block. Lines stream until a line that is exactly `[exoutput]`:

```
[  output]
line 1
line 2
...
[exoutput]
```

All output goes to stdout. stderr is reserved for future use.

Tag colors (ANSI, applied to the full tag including brackets):

| Tag          | Color       | ANSI code |
|--------------|-------------|-----------|
| `[  output]` | light green | `\033[92m` |
| `[exoutput]` | dark green  | `\033[32m` |
| `[progress]` | yellow      | `\033[33m` |
| `[   error]` | red         | `\033[31m` |

Format: `fmt.Sprintf("\033[XXm[%8s]\033[0m", label)`

## Workflow

After making changes, always run `npm run build` and `npm run vet` to verify a clean build before considering the work done.
