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
$ fern -O -target x86-64-linux -o diff3 diffutils/diff3.fern
$ ./diff3 -m mine older yours
$ fern -O -target x86-64-linux -o sdiff diffutils/sdiff.fern
$ ./sdiff -o merged a b
```

## Programs

| Program | Status | Issue |
|---|---|---|
| `cmp` | Done. All options. | #15586 |
| `diff` | Done. All options. Refer to [diff](#diff). | #15586 |
| `diff3` | Done. All options. Refer to [diff3](#diff3). | #15586 |
| `sdiff` | Done. All options, with the interactive merge. Refer to [sdiff](#sdiff). | #15586 |

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

The port has each option of GNU diff 3.10. These are the formats:

- Normal, context (`-c`, `-C`) and unified (`-u`, `-U`), with the
  obsolete `-NUM` form of the context.
- Ed (`-e`), forward ed (`-f`) and RCS (`-n`).
- Side by side (`-y`), with `-W`, `--left-column` and
  `--suppress-common-lines`.
- `-D`, and the group and line formats with each `%` directive.

These are the other options:

- The output changes: `-p`, `-F`, `-t`, `-T`, `--tabsize`,
  `--suppress-blank-empty`, `-l`, `--color`, `--palette` and `-L`.
- The file selection: `-r`, `-N`, `-P`, `-x`, `-X`, `-S`, `--from-file`,
  `--to-file`, `--no-dereference` and `--ignore-file-name-case`.
- The input changes: `-i`, `-b`, `-w`, `-E`, `-Z`, `-B`, `-I`, `-a` and
  `--strip-trailing-cr`.
- `-q`, `-s`, `-d`, `-H` and `--horizon-lines`.
- The hidden options `--sdiff-merge-assist` and `---no-directory`, which
  sdiff and diff3 give to diff.
- `--binary`, `-h` and `--inhibit-hunk-merge`, which have no effect.

GNU diff 3.10 also gives the same output with and without
`--inhibit-hunk-merge`.

`-l` sends the output of each comparison to `/usr/bin/pr -h HEADER`, as
GNU diff does. The messages, such as "Only in", come after all the pages.
On WASI, `-l` fails with "could not be invoked", because WASI cannot
start a process.

GNU diff 3.10 differs from its manual in some places. The port does as
the binary does:

- With `-E` and `-Z` together, the tabs are not expanded.
- In the ed formats, a file without a final newline gives the error
  "No newline at end of file" and exit status 2. The last line then
  compares as a complete line.
- When only `--changed-group-format` is given, the old and new groups
  also use it.
- In `--palette`, `^?` gives a DEL and then a `?`.

## diff3

The port of diff3 has each option of GNU diff3 3.10: `-e`, `-E`, `-x`,
`-X`, `-3`, `-m`, `-A`, `-i`, `-a`, `-T`, `--strip-trailing-cr`, `-L` and
`--diff-program`. The exit status is 0, 1 when a block is a conflict, and
2 for trouble.

GNU diff3 runs diff two times and reads its normal output. The port runs
its own diff in the same process and reads the same output. Thus an
error of diff gives the messages of GNU diff3. `--diff-program` runs the
program in a child process. On WASI that option fails.

GNU diff3 3.10 differs from its manual in two places. The port does as
the binary does. `-X` does not bracket the conflicts, so it is the same
as `-x`. A merge of a file without a final newline writes its last line
with no newline before the next marker.

## sdiff

The port of sdiff has each option of GNU sdiff 3.10, with the
interactive merge of `-o`.

Without `-o`, GNU sdiff runs `diff -y` with the options that it gives to
diff. The port runs its own diff with the same arguments. The output,
the diagnostics and the exit status are thus those of diff. GNU sdiff
finds diff in `PATH`, and the port does not. `--diff-program` runs the
program in place of sdiff, as GNU sdiff does.

With `-o`, the port runs `diff --sdiff-merge-assist` in a child process.
Then it reads the commands of the user from standard input: `l`, `r`,
`1`, `2`, `s`, `v`, `q`, and `e` in each of its forms. The `e` commands
run the program of `EDITOR` (by default `ed`) on a temporary file in
`TMPDIR`. On WASI, the merge runs diff in the same process. There, the
`e` commands fail, and an error of diff stops sdiff before its own
message.

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

The tables below are the best of five runs on a 4-core x86-64 container
that other jobs share, 2026-11-14. The files are in the page cache, and
each port gives the same output as the GNU program. The inputs:

- r1, r2: 1,000,000 lines (28 MB each), with 1% of the lines changed.
- big0: 1,000,000 lines (40 MB). big1 and big2 each change, delete or
  insert 1% of the lines of big0, at other places.
- un0, un1, un2: unrelated files of 10,000 lines from 32 words.

On this container the time of one input can change by 20% from one set
of runs to the next. In one set of runs on r1 and r2, the diff of
`origin/main` before this work took 0.303 s. This diff took 0.244 s, and
GNU diff took 0.186 s.

| Command | GNU 3.10 | Fern |
|---|--:|--:|
| `diff r1 r2` | 0.196 s | 0.254 s |
| `diff -u r1 r2` | 0.210 s | 0.263 s |
| `diff big1 big2` | 0.237 s | 0.318 s |
| `diff un0 un1` | 0.165 s | 0.255 s |
| `diff -e big1 big2` | 0.216 s | 0.328 s |
| `diff -f big1 big2` | 0.222 s | 0.314 s |
| `diff -n big1 big2` | 0.224 s | 0.327 s |
| `diff -y big1 big2` | 0.362 s | 0.463 s |
| `diff -y un0 un1` | 0.169 s | 0.262 s |
| `diff -DX big1 big2` | 0.224 s | 0.341 s |
| `diff --line-format='%dn %L' big1 big2` | 0.290 s | 0.393 s |
| `diff -u -p big1 big2` | 0.241 s | 0.328 s |
| `diff --color=always -u big1 big2` | 0.229 s | 0.340 s |
| `diff -E big1 big2` | 0.385 s | 0.317 s |
| `diff -Z big1 big2` | 0.485 s | 0.416 s |
| `diff -I changed big1 big2` | 0.223 s | 0.328 s |
| `diff3 big1 big0 big2` | 0.463 s | 0.566 s |
| `diff3 -m big1 big0 big2` | 0.557 s | 0.583 s |
| `diff3 -e big1 big0 big2` | 0.451 s | 0.543 s |
| `diff3 un0 un1 un2` | 0.353 s | 0.506 s |
| `sdiff big1 big2` | 0.366 s | 0.397 s |
| `sdiff un0 un1` | 0.162 s | 0.249 s |

The ports are slower than GNU except for `-E` and `-Z`. The cost is in
the diff engine, which each format and diff3 and sdiff share. The format
code adds less than the engine for each command.

On r1 and r2 the port runs 1.00 G instructions and GNU diff runs 0.67 G
(callgrind). The port also reads 435 M words of data, and GNU diff reads
198 M. Native `i32` arrays use eight-byte slots. Thus each table of line
numbers and classes is two times as large as in GNU diff. The port uses
193 MB at the peak, and GNU diff uses 138 MB.

The time of the unrelated files is in the search for the middle snake, the
`diag` function. There the port runs 4.07 G instructions and GNU diff runs
2.27 G (callgrind, two files of 10,000 lines from 32 words). Nearly every
diagonal reads the snake guard once and stops, so the cost is in the loop
over the diagonals. Each diagonal takes about 54 instructions in the port
and 32 in GNU diff:

- A bounds check on each array access, about 11 instructions. The start of
  a snake is a value that the loop reads from `v`, so no guard bounds it.
  The compiler cannot remove these checks without a proof that it does not
  have.
- A sign extension after each `i32` addition, 4 instructions. An `i32`
  wraps, so the extension is necessary. GNU diff uses 64-bit offsets.
- Loads and stores of loop values in stack slots, about 6 instructions.
  A value holds its register over its whole interval, also where it is
  dead. Interference that follows liveness can remove more of these
  (#15650).

These are causes in the compiler and its array layout, and this work does
not remove them.
