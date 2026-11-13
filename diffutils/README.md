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
$ fern -O -target x86-64-linux -o diff diffutils/diff.fern
$ ./diff -u a b
```

## Programs

| Program | Status | Issue |
|---|---|---|
| `cmp` | Done. All options. | #15586 |
| `diff` | Done for the normal, context and unified formats. Refer to [diff](#diff). | #15586 |
| `diff3`, `sdiff` | Not started. | #15586 |

## diff

The port does the steps of GNU diff in the same order. Thus it selects the
same hunks as GNU diff, also when more than one shortest diff is possible:

1. It removes the identical prefix and suffix of the two files at line
   boundaries. It keeps `--horizon-lines` lines of each, and not fewer
   lines than the context.

2. It gives each line a class. Two lines get the same class when they
   compare equal under `-i`, `-b` and `-w`. A last line without a newline
   is in a class of its own, except under `-b` and `-w`.

3. It removes the lines that match no line of the other file from the
   search, as `discard_confusing_lines` does.

4. It finds a shortest edit script with the Myers O(ND) algorithm. It uses
   the cost limit of GNU diff, and the heuristic of `-H`. `-d` disables
   both and the step before.

5. It moves each run of changes as `shift_boundaries` does.

These options are in the port:

- The formats: normal (`--normal`), context (`-c`, `-C NUM`,
  `--context[=NUM]`) and unified (`-u`, `-U NUM`, `--unified[=NUM]`), with
  the obsolete `-NUM` form of the context.
- `-q`, `-s`, `-i`, `-b`, `-w`, `-B`, `-a`, `-N`, `-P`, `-r`, `-L`
  (`--label`), `-d`, `-H` and `--horizon-lines`.
- `--binary`, `--palette`, `--no-ignore-file-name-case` and `-h`. They have
  no effect, as in GNU diff without `--color`.

These options are not in the port:

- The other formats: `-e`, `-f`, `-n`, `-y`, `-W`, `--left-column`,
  `--suppress-common-lines`, `-D` and the group and line formats.
- The output changes: `-p`, `-F`, `-t`, `-T`, `--tabsize`,
  `--suppress-blank-empty`, `-l` and `--color`.
- The file selection: `-x`, `-X`, `-S`, `--from-file`, `--to-file`,
  `--no-dereference` and `--ignore-file-name-case`.
- The input changes: `-E`, `-Z`, `-I` and `--strip-trailing-cr`.
- `--inhibit-hunk-merge` and `--sdiff-merge-assist`.

The port parses these options as GNU diff does. Thus a prefix and a
conflict of output styles give the same errors. Then the port stops with
exit status 2 and the message `this port does not have the option
'--NAME'`.

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

`diff` on two files of 1,000,000 lines (23 MB each) with 1% of the lines
changed. Also `diff` on two unrelated files of 10,000 lines from a small set
of words. The files are in the page cache. The value is the best of five
runs on a 4-core x86-64 container, 2026-11-13. The two programs give the
same output. On this container, the times of one input can change by 20%
from one set of runs to the next.

| Input | GNU diff 3.10 | Fern diff |
|---|--:|--:|
| 1,000,000 lines, 1% changed | 0.334 s | 0.266 s |
| 1,000,000 lines, 1% changed, `-u` | 0.323 s | 0.267 s |
| 10,000 unrelated lines | 0.111 s | 0.227 s |
| 10,000 unrelated lines, `-u` | 0.135 s | 0.214 s |

The port is faster on the large files. Most lines of the second file get
their class without a hash table lookup. The port first compares
each line with the line after the last match. A lookup in a table of
2,000,000 lines is a cache miss.

The time of the unrelated files is in the search for the middle snake, the
`diag` function. There the port runs 4.70 G instructions and GNU diff runs
2.61 G (callgrind). The causes are in the code that the compiler makes for
the loop:

- A bounds check on each array access.
- A sign extension after each `i32` addition.
- Loop values that stay in stack slots.

The fix is in the compiler. It must remove the bounds check of an index
that a loop guard keeps below the length. Its register allocation must keep
the loop values in registers.

Native `i32` arrays use eight-byte slots. Thus the port uses more memory:
201 MB at the peak for the large files, where GNU diff uses 125 MB.
