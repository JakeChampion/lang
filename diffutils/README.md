# diffutils

GNU diffutils, written again in Fern. Each program gives the same output,
the same error messages and the same exit codes as GNU diffutils 3.10.

The programs use the shared library of the coreutils ports,
`coreutils/lib/gnu.fern`. The parity gate is the coreutils corpus in
`internal/testing/coreutils/`. `docs/COREUTILS.md` has the rules of parity
and the method.

## Build

```
$ fern -O -target x86-64-linux -o cmp diffutils/cmp.fern     # or arm64-linux, arm64-darwin
$ ./cmp a b
a b differ: char 8, line 2
$ ./cmp --version
cmp (Fern diffutils) 0.1
```

## Programs

| Program | Status | Issue |
|---|---|---|
| `cmp` | Done. All options. | #15586 |
| `diff` | Not started. | #15586 |
| `diff3`, `sdiff` | Not started. | #15586 |

## Tests

The corpus of each program is `internal/testing/coreutils/PROGRAM_test.go`.
The gate runs each case three times:

- The port and the GNU program, byte for byte.
- The port from the self-host compiler and the port from the release compiler.
- The port with the leak census on.

The oracle is the GNU program in `FERN_GNU_DIFFUTILS`, a PATH-style list.
Without it, the harness looks in `PATH`, `/usr/bin`, `/bin` and
`/usr/local/bin`. CI builds GNU diffutils 3.10 from source with
`.github/actions/gnu-diffutils`.

## Performance

`cmp` on two files of 400 MiB that differ at the last byte, from the page
cache. The value is the best of seven runs on a 4-core x86-64 container,
2026-11-13.

| Program | Text file | Random bytes |
|---|--:|--:|
| GNU cmp 3.10 | 0.135 s | 0.130 s |
| Fern cmp | 0.118 s | 0.123 s |
