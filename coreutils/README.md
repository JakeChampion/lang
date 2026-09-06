# coreutils

GNU coreutils, reimplemented in Fern, held to byte-for-byte parity with the
GNU binaries and measured against them and against uutils. The definition of
parity, the method, and the plan are in `docs/COREUTILS.md`; this file is
the how-to.

These are not the demos in `examples/cli/`. Those show the CLI shape of the
language with a subset of each tool's flags; these are the real thing, with
every flag, every error message and every exit code, and a test that proves
it against the reference implementation.

## Build

```
$ fern -O -target arm64-darwin -o yes coreutils/yes.fern     # or arm64-linux, x86-64-linux
$ ./yes | head -3
y
y
y
$ ./yes --vers
yes (Fern coreutils) 0.1
$ ./yes -x
./yes: invalid option -- 'x'
Try './yes --help' for more information.
```

Every utility is a single static binary with no runtime dependencies. Build
them all:

```
$ for f in coreutils/*.fern; do fern -O -target arm64-linux -o "bin/$(basename "$f" .fern)" "$f"; done
```

## Test

```
$ go test ./internal/coreutils/
```

The gate runs each utility against the GNU binary of the same name over a
corpus of invocations and diffs stdout, stderr and exit status. It needs GNU
coreutils: on Linux that is the system one; on macOS the system tools are
BSD, so point it at a GNU install:

```
$ FERN_GNU_COREUTILS=/nix/store/…-coreutils-9.10/bin go test ./internal/coreutils/
```

Not finding one is a failure, deliberately — a parity gate that passes when
it cannot find its oracle is worse than no gate.

## Bench

```
$ scripts/coreutils-bench                 # every utility, table to stdout
$ scripts/coreutils-bench -o bench.md yes # one utility, table also to a file
```

hyperfine over Fern (built with `-O`), GNU and uutils on the same workloads;
`FERN_GNU_COREUTILS` and `FERN_UUTILS` name the reference directories when
they are not on PATH. Wall time, so compare within one run only.

## What is here

| utility | status |
|---|---|
| `true` `false` | done |
| `yes` | done — one write per 4 KiB block |
| `echo` | done — `-n` `-e` `-E`, the full escape table, POSIXLY_CORRECT |
| `basename` | done — `-a` `-s` `-z`, in-order option scan, proper-suffix rule |
| `dirname` | done — `-z`, the leading-slash rules |
| `tsort` | done — GNU's exact order, the loop report, NUL-cut tokens |
| `printf` | done — every C conversion plus `%b` `%q`, argument cycling, long-double floats printed from the exact binary value at the target's format, glibc's oversized-conversion refusal and close_stdout reporting |
| `expr` | done — the full grammar, arbitrary-precision integers, `:` / `match` over POSIX basic regexps with glibc's diagnostics, exit 0/1/2/3 |
| `numfmt` | done — every option, the long-double arithmetic GNU scales and rounds in, the field and padding rules |
| `seq` | done — `-f` `-s` `-w`, the exact-digit engine for whole numbers, and the long-double one with the rule that prints a term past LAST when its own output reads back inside the range |
| `factor` | done — Montgomery arithmetic to 2^64, `core/bigint` beyond it, `-h`, numbers from stdin, and GNU's unbuffered line for a number at or above 2^127 |
| `sleep` | done — `s` `m` `h` `d`, floats and hex floats, `inf`, the sum of the operands, every operand validated before it pauses (intervals are rounded up to the millisecond until #8528) |
| `head` | done — `-c` `-n` with the leading-minus elisions and gnulib's multiplier suffixes, `-q` `-v` `-z`, the obsolete `-NUM[bkmclqvz]` form, and the hidden `---presume-input-pipe` |
| `wc` | done — `-c` `-l` `-m` `-w` `-L`, `--total=WHEN`, `--files0-from`, the column width taken from the operands' sizes, and the C-locale ISPRINT rule for words and display width. A REGULAR-FILE stdin still widens to seven columns, which needs fstat on a descriptor (#8713) |
| `test` `[` | done — POSIX's one-to-four-argument table and GNU's parser beyond it, every string, integer (any length, compared as digit strings), file and file-pair primary, `-l STRING`, `-t` via isatty, `-r -w -x` against the effective ids; `[` adds the closing `]` and honours `--help` / `--version` as the sole argument where `test` does not. Needs `stat`, `access`, `geteuid` and `isatty`, so it is a native-target utility: WASI reports no mode, owner or effective ids |
| `lib/gnu.fern` | the GNU conventions every utility shares |
| `lib/cond.fern` | the conditional expression `test` and `[` evaluate |
| `lib/bre.fern` | POSIX basic regular expressions as glibc compiles them |
| `lib/ld.fern` | C's `long double` at the TARGET's format — strtold, arithmetic, rounding and the `%f` `%e` `%g` `%a` conversions — shared by `printf`, `numfmt`, `seq` and `sleep` |

The tracking epic (#8278) lists every other utility and its status.
