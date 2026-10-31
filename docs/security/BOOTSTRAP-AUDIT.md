# Bootstrap and compiler self-consistency

Status: audit for [SEC-045](https://github.com/JakeChampion/lang/issues/13547).
Parent: [security epic](https://github.com/JakeChampion/lang/issues/13502).
The chain is described in `docs/BOOTSTRAP.md`, and the reproducibility test in [REPRODUCIBILITY.md](REPRODUCIBILITY.md).

## What exists

The pin is `bootstrap/stage0.lock`: the commit the stage0 was built from, the release URL, and the sha256 of the uncompressed stage0 binary per host.
`bootstrap/bootstrap.sh` downloads the asset, decompresses it, and compares the sha256 with the lock before the binary runs.
A mismatch, an asset that is not gzip, and a stage0 that refuses the source each stop the build with a cause. A stage1 that fails its smoke tests stops it too.

`make distcheck` builds stage1 with the stage0, stage2 with stage1, and stage3 with stage2, and requires stage2 and stage3 to be byte-identical.
The fixpoint runs in CI on x86-64 Linux, arm64 Linux and Apple Silicon.

| Test | What it pins |
|---|---|
| `TestPinnedStage0IsDownloadedVerifiedAndCached` | the download, the hash check and the cache |
| `TestPinnedStage0WithWrongHashIsRejected` | an altered asset fails with `sha256 mismatch` |
| `TestPinnedStage0ThatIsNotGzipIsRejectedAndCleanedUp` | a substituted asset fails and leaves nothing behind |
| `TestStage0RefusingTheSourceFails`, `TestBuildRejectsACompilerWhoseOutputDoesNotRun`, `TestBuildRejectsBrokenMultiModuleSmoke` | a stage that cannot build or run the smoke programs stops the chain |
| `TestDistcheckReachesFixedPoint`, `TestDistcheckDivergentStagesFail` | the fixpoint holds, and a divergence fails |
| `TestSelfHostReproducibleAcrossPaths` | the images are byte-identical across source roots, directories, locales and environments, and a `-g` image is identical with `-remap-path-prefix` |

## The limit of self-consistency

The fixpoint proves that the compiler reproduces itself.
It does not prove that the compiler is honest.
A compiler that recognises its own source and inserts the same change into every stage reproduces itself exactly. That is the trusting-trust attack. The sha256 in the lock only proves that the stage0 is the binary someone published.

The stage0 is a previous self-built compiler.
The chain of trust therefore reaches back through every pin to the Go compiler of 2026-09-28.
That compiler is the last native one that did not descend from a Fern binary.

## Diverse double compilation

Diverse double compilation needs a second compiler, of independent origin, that compiles the Fern compiler's source.
Its output compiled once more by itself must match the stage2 of the chain.

The Go oracle is an independent implementation of the parser, the checker and the interpreter, and it has no native backend.
It can run the Fern compiler's own source as a program: `fern-oracle -embed build/embed -interp compiler/fern_main.fern -- -target x86-64-linux -emit asm ...` accepts the source, loads the embedded assets, and starts compiling.
On this host that run did not finish a 40-line program in 25 minutes of processor time. The native compiler takes a fraction of a second. The interpreter is therefore a second compiler in principle and not in practice.

The feasible path is a diverse stage: the interpreter compiles one small module per run, and the outputs are compared with the native compiler's for the same module.
That checks the backend's output for the modules it reaches, not the whole stage. The whole compiler needs an interpreter that is faster by two orders of magnitude.
No second native compiler exists.

```sh
go test ./internal/testing/bootstrap -count=1 -timeout 0
go test ./internal/testing/e2ecompiler -run 'TestSelfHostReproducibleAcrossPaths' -count=1 -timeout 0
```

## Remaining limits

- Diverse double compilation of the whole compiler is not feasible with the interpreter's speed, as measured above.
- The pin's provenance is a release asset on GitHub. There is no signature on the asset beyond the hash in the lock, which the repository history carries.
- A `-g` image is not reproducible across directories.
