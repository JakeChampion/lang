# Fuzzing the compiler's later boundaries

Status: [reference] audit and targets for [SEC-040](https://github.com/JakeChampion/lang/issues/13542).
Parent: [security epic](https://github.com/JakeChampion/lang/issues/13502).
The generators, the differential oracles and the mutation score are in `docs/FUZZING.md`.

## What existed

`FuzzParse` and `FuzzCheck` drive the Go parser and checker in process, with committed corpora.
`FuzzSelfHostFrontEnd` drives `fern -check` on hostile source and must see a diagnostic, never a signal or a hang.
`FuzzGenerate_ExecutionAgrees`, `FuzzGenerate_StdoutAgrees` and `FuzzGenerate_ParseRoundTrips` drive fernsmith programs through the oracles.
Nothing fed hostile bytes to the boundaries after the front end: the IR verifiers, the in-process assemblers, and the readers of a native image.

## The new targets

| Target | Boundary | Driver | Accepted outcomes |
|---|---|---|---|
| `FuzzSelfHostManifest` | `fern -manifest` reads a native image | the CLI | the fields in printable ASCII with exit 0, or `fern: no build manifest in` with exit 1 |
| `FuzzSelfHostIRVerify` | the structural, stack and reference-count verifiers read an op stream | `compiler/drivers/irverify_fuzz_run.fern` | a problem count from each verifier, exit 0 |
| `FuzzSelfHostX86Assembler` | the x86-64 assembler reads assembly text | `compiler/drivers/asm_fuzz_run.fern x86` | a summary with exit 0, or an abort with a `fern:` cause |
| `FuzzSelfHostArm64Assembler` | the arm64 assembler reads assembly text | `compiler/drivers/asm_fuzz_run.fern arm64` | the same |
| `FuzzSelfHostToml` | the manifest reader and its diagnostic formatter read a `fern.toml` | `compiler/drivers/pkg_fuzz_run.fern toml` | a summary with exit 0 |
| `FuzzSelfHostLock` | the lock reader reads a `fern.lock` | `compiler/drivers/pkg_fuzz_run.fern lock` | the same |
| `FuzzSelfHostIndex` | the index reader reads a registry `index.toml` | `compiler/drivers/pkg_fuzz_run.fern index` | the same |
| `FuzzSelfHostArchive` | the unpacker reads a package archive: the gunzip, the tar reader and the entry name rules | `compiler/drivers/pkg_fuzz_run.fern archive` | the same |

The verifier driver decodes the input bytes into ops.
The first two bytes choose the parameter and local counts.
Every later group of four bytes is one op: a kind up to 511, a small signed immediate, a width with its sign flag, and a callee name with a declaration index.
Unknown kinds and unknown callees are part of the input space, because the verifiers must refuse them.

The package readers take the bytes an attacker controls when a program depends on a package: the manifest and lock of a dependency, the index a manifest names, and the archive the index points at.
Each reader refuses a malformed input with a message, so the one accepted outcome is exit 0 with the reader's summary.
The archive target unpacks into a directory of its own under the input's directory. Its seeds carry an entry that escapes the package directory, a long name and a truncated archive.

Every input runs in a child process under two budgets, 20 seconds of wall time and 512 MiB of resident memory.
A hang, a signal or a budget breach fails the input whatever the exit status.
An input over 64 KiB is skipped.
The seeds are in each target's `f.Add` list. They include a window of a real image around its manifest, a manifest line without its newline, and the magic repeated. They also include the compiler's own emitted text for a small program, malformed directives, and empty input.

## Results on this host

Host: Linux 6.18.44-fc-v165, Intel(R) Xeon(R) Processor @ 2.80GHz, Go go1.27.3.
Each target ran 45 seconds after its seeds passed.

| Target | Executions | New inputs kept | Findings |
|---|---|---|---|
| `FuzzSelfHostManifest` | 33105 | 2 | none |
| `FuzzSelfHostIRVerify` | 22414 | 0 | none |
| `FuzzSelfHostX86Assembler` | 18168 | 2 | none |
| `FuzzSelfHostArm64Assembler` | 39065 | 3 | none |

The package readers ran 45 seconds each on 2026-11-08 on the same kind of host, after their seeds passed.

| Target | Executions | New inputs kept | Findings |
|---|---|---|---|
| `FuzzSelfHostToml` | 33497 | 1 | none |
| `FuzzSelfHostLock` | 31774 | 1 | none |
| `FuzzSelfHostIndex` | 31382 | 0 | none |
| `FuzzSelfHostArchive` | 56102 | 2 | none |

An abort with a cause is an accepted outcome for the assemblers. They consume the compiler's own output, and a bounds abort on foreign text is the language's defined trap.
The run logs are in the pull request.

## Crash triage

`go test -fuzz` minimises a failing input and writes it under `internal/testing/e2ecompiler/testdata/fuzz/<Target>/`.
Commit that file with the fix, so the per-PR run replays it.
The failure message names the budget that broke, the signal, or the exit status and the output.
A hang names the budget, and a budget breach names the resident size.

## In CI

`fuzz-diff.yml` runs the eight targets for 30 seconds each on the x86-64 runner, from the committed seeds, after the differential targets.
The drivers run natively, so the arm64 runner does not run them.
The `fuzz` job of `nightly-fuzz.yml` runs each of the eight targets on the x86-64 runner for 20 minutes.
Each target starts from a cached corpus that grows from night to night.

## Limits

- No coverage feedback: Go's instrumentation does not see into a Fern process, so the mutations are blind. The IR driver's dense encoding keeps every mutation a valid op stream, which is what makes blind mutation reach the verifiers.
- The memory budget is a resident-size ceiling on the child, not a per-allocation count.
- The ELF writer and the linker take only what the assemblers produce, and no target feeds them foreign input.
