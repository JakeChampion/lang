# Reproducible images

Status: audit and test for [SEC-043](https://github.com/JakeChampion/lang/issues/13545).
Parent: [security epic](https://github.com/JakeChampion/lang/issues/13502).
The bootstrap fixpoint is recorded in `docs/BOOTSTRAP.md`.

## What exists

`make distcheck` builds the compiler three times and requires the second and the third image to be byte-identical.
That proves the compiler reproduces itself, from one directory and one environment, on all three hosts in CI.
It does not vary the directory or the environment, so a path or an environment variable baked into an image passes it.

## Verification

`TestSelfHostReproducibleAcrossPaths` compiles one program twice for each target.
Each compile has its own source root and its own copy of the stdlib.
Each compile also has its own working directory, two levels deeper for the second.
The two compiles also use different `HOME`, `USER`, `LANG`, `LC_ALL`, `TZ` and `SOURCE_DATE_EPOCH` values and a different probe variable.
The images must be byte-identical:

| Target | Images compared |
|---|---|
| x86-64-linux | plain, `-pie`, `-harden` |
| arm64-linux | plain, `-pie`, `-harden` |
| arm64-darwin | plain |
| arm64-android | plain |
| wasm32-wasi | plain component |

The `-g` images are compared twice on x86-64-linux, arm64-linux and arm64-darwin.
Without `-remap-path-prefix` the images must differ. This is the negative control: it shows that the test sees the paths.
The second comparison uses `-remap-path-prefix` for the source root and the working directory.
Then the images must be byte-identical and must not contain the original root.

`TestSelfHostRemapPathPrefix` pins how the flag reads its value and which prefix it replaces. It also pins the refusal of a malformed value.

The program reaches string literals, a constant aggregate, a runtime module with its own literals, and the clock. That exercises every data table the writer lays out.

```sh
go test ./internal/testing/e2ecompiler -run 'TestSelfHostReproducibleAcrossPaths|TestSelfHostRemapPathPrefix' -count=1 -timeout 0
```

## Paths in a debug image

A `-g` image on x86-64-linux, arm64-linux and arm64-darwin holds two kinds of path in its DWARF:

- The compile directory, which is the working directory of the compiler.
- The entry file name, as the command line gives it, and the directory of that file.

Stdlib files are named `stdlib://std/...` and `stdlib://core/...`, so the stdlib root does not go into an image.

`-remap-path-prefix FROM=TO` replaces the `FROM` prefix of each of these paths with `TO`.
`FROM` matches whole path components only, so `/a` does not match `/ab`.
A slash at the end of `FROM` has no effect.
When more than one `FROM` matches, the last flag on the command line wins, as with `rustc --remap-path-prefix`.
An empty `TO` makes the path relative.
A value with no `=` or with an empty `FROM` stops the compile with exit status 2.

A debugger finds the sources through its own path substitution, for example `set substitute-path /src /home/me/project` in gdb.

## Inputs that change an image

These inputs change the image, and a reproducible build must hold them fixed:

- The source text of the program, its packages and the stdlib.
- The compiler binary.
- The command-line flags.
- Environment variables named `FERN_*` that select a build mode, for example `FERN_SIZE`, `FERN_SANITIZE`, `FERN_BACKTRACE` and `FERN_COVER`.
- Under `-g` only, the paths that the section above names.

## Inputs that do not change an image

- The clock. The compiler reads the monotonic clock only for reports of compile time on stderr. It does not read the wall clock.
- The locale. The compiler does not read `LANG` or `LC_*`, and its output of numbers and text does not use the locale.
- The hash seed of a string-keyed map. Each process takes a random seed.
But a map iterates in the order of its storage, not in the order of its hash buckets. Each compile in the test is a separate process with its own seed.
- `HOME`, `XDG_CACHE_HOME` and `FERN_CACHE_DIR`. They select the package store. The package sources go into the image, but the store path goes in only under `-g`, where `-remap-path-prefix` maps it.
- `NO_COLOR`, `FERN_DIAG_FORMAT`, `TMPDIR` and `PATH`. They change diagnostics, temporary files and `-run` only.

## Remaining limits

- The test does not vary the host, the kernel, or the toolchain. The three CI hosts cover that for the compiler alone, through `make distcheck`.
- The arm64 rows run under QEMU user mode on an x86-64 host. That is emulated evidence only.
- `-g` writes no DWARF for arm64-android and for the WASI targets. Those images are the same with and without `-g`.
- A diverse second compiler for a diverse double-compile is not available, which SEC-045 records.
