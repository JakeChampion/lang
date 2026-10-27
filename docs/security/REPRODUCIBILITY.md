# Reproducible images

Status: audit and test for [SEC-043](https://github.com/JakeChampion/lang/issues/13545).
Parent: [security epic](https://github.com/JakeChampion/lang/issues/13502).
The bootstrap fixpoint is recorded in `docs/BOOTSTRAP.md`.

## What exists

`make distcheck` builds the compiler three times and requires the second and the third image to be byte-identical.
That proves the compiler reproduces itself, from one directory and one environment, on all three hosts in CI.
It does not vary the directory or the environment, so a path or an environment variable baked into an image passes it.

## Verification

`TestSelfHostReproducibleAcrossPaths` compiles one program twice on each native target.
The two compiles run from different working directories, one of them two levels deep. They also run with different `HOME`, `USER` and `TZ` values and a different probe variable.
The plain, `-pie` and `-harden` images must be byte-identical, and they are on x86-64-linux and on arm64-linux.

A `-g` image is checked for a difference instead, and it differs.
The DWARF carries the compile directory, which `comp_dir()` reads from the working directory, so a `-g` image reproduces only from the same directory.
The program reaches string literals, a constant aggregate, a runtime module with its own literals, and the clock. That exercises every data table the writer lays out.

```sh
go test ./internal/testing/e2ecompiler -run 'TestSelfHostReproducibleAcrossPaths' -count=1 -timeout 0
```

## Remaining limits

- A `-g` image embeds the compile directory. A fixed compile directory, as `-fdebug-prefix-map` provides elsewhere, is not implemented.
- The test varies the directory and four environment variables. It does not vary the host, the kernel, or the toolchain, which the three CI hosts cover for the compiler alone.
- A diverse second compiler for a diverse double-compile is not available, which SEC-045 records.
