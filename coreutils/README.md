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
| `cat` | done — `-A` `-b` `-e` `-E` `-n` `-s` `-t` `-T` `-u` `-v`, line state carried across files, and the two fstat checks GNU makes before a byte moves: a closed stdout is reported even with nothing to copy, and an input that is also the output is `input file is output file` |
| `tail` | done — `-c` `-n` with the leading-plus form and gnulib's multiplier suffixes, `-q` `-v` `-z`, the obsolete `[+-]NUM[bcl][f]` form under all three `_POSIX2_VERSION` regimes, and following: `-f` `-F` `--follow[=HOW]` `--retry` `-s` `--max-unchanged-stats`, with file truncated / appeared / replaced / no files remaining in GNU's words. A regular file is read from its end by seeking. `--pid` needs a process-liveness primitive (#8767), so it takes GNU's own not-supported-on-this-system path |
| `tac` | done — `-b` `-r` `-s`, the input read BACKWARDS in 8 KiB blocks that double when a record outgrows one, and `-r` in glibc's syntax 0 (Emacs), which is what a program that never calls `re_set_syntax` gets. Startup beats GNU by 4×; throughput loses by 3-4×, and `docs/COREUTILS.md` measures where it goes |
| `wc` | done — `-c` `-l` `-m` `-w` `-L`, `--total=WHEN`, `--files0-from`, the column width taken from the operands' sizes, and the C-locale ISPRINT rule for words and display width. A REGULAR-FILE stdin still widens to seven columns, which needs fstat on a descriptor (#8713) |
| `nl` | done — `-b` `-h` `-f` with the `a` / `t` / `n` / `pBRE` styles, `-d` in all three delimiter forms, `-i` `-l` `-n` `-p` `-s` `-v` `-w`, and the line-number overflow reported one line after it happens |
| `join` | done — `-1` `-2` `-j` `-a` `-v` `-e` `-o` `-t` `-i` `--check-order` `--nocheck-order` `--header` `-z`, the obsolete `-j1` / `-j2` and multi-argument `-o` forms with the counting that tells an option argument from a file, and GNU's order check with its default weakness |
| `tr` | done — the whole SET grammar (ranges, `[:class:]`, `[=c=]`, `[c*n]`, `[c*]`), `-c` `-C` `-d` `-s` `-t`, the case-fold pairing of `[:upper:]` and `[:lower:]`, and the lazy SET2 extension that decides which of two faults a mismatched pair is reported as |
| `hostid` | done — glibc's gethostid: `/etc/hostid`, else the hostname's IPv4 address through NSS (`lib/resolv.fern`: nsswitch's `hosts:` line, `/etc/hosts`, `/etc/resolv.conf`, an RFC 1035 A query over TCP) with its halves swapped, else 0; `extra operand` for anything. Needs `hostname()`, so it is a native-target utility: WASI has no host identity |
| `base64` `base32` | done — `-d` `-i` `-w`, gnulib's group rules (a fault still writes the bytes the group completed), and the `-w` value's two GNU quirks: a negative is refused, a magnitude past INTMAX_MAX turns wrapping off |
| `basenc` | done — `--base64` `--base64url` `--base32` `--base32hex` `--base16` `--base2msbf` `--base2lsbf` `--z85`, `missing encoding type`, and z85's block requirement on both sides |
| `uniq` | done — `-c` `-d` `-D` `-u` `-i` `-z`, `-f` / `-s` / `-w` and how they compose, `--all-repeated` and `--group` with every separator method, the obsolete `-N` / `+N` operands and the `_POSIX2_VERSION` window that retires the second of them, and the OUTPUT operand, which is a file opened truncating before the input is read. Slower than GNU per line while a substring comparison costs a copy (#8791) |
| `comm` | done — `-1` `-2` `-3` `-z`, `--total`, `--output-delimiter` including the empty one (a NUL) and its refusal of a second, differing one, and the order check in both its shapes: `--check-order` is fatal at the first fault, the default stays silent until an unpairable line has been seen and re-examines a file's last pair at end of input. `comm - -` is ONE stream read alternately, closed twice. Slower than GNU while the order check walks bytes (#8791) |
| `link` | done — one `link(2)`: exactly two operands, no options of its own, and a symlink named as FILE1 linked to ITSELF rather than to what it points at |
| `unlink` | done — one `unlink(2)`: exactly one operand, a directory refused with `Is a directory`, and a symlink removed itself rather than followed |
| `test` `[` | done — POSIX's one-to-four-argument table and GNU's parser beyond it, every string, integer (any length, compared as digit strings), file and file-pair primary, `-l STRING`, `-t` via isatty, `-r -w -x` against the effective ids; `[` adds the closing `]` and honours `--help` / `--version` as the sole argument where `test` does not. Needs `stat`, `access`, `geteuid` and `isatty`, so it is a native-target utility: WASI reports no mode, owner or effective ids |
| `md5sum` `sha1sum` `sha224sum` `sha256sum` `sha384sum` `sha512sum` | done — `-b` `-c` `-t` `-z` `--tag`, the whole `-c` report (`OK`, `FAILED`, `FAILED open or read`, the improperly-formatted warning and its summary) with `--ignore-missing` `--quiet` `--status` `--strict` `-w`, and GNU's file-name escaping in both directions: a backslash, a newline or a carriage return leads the line with `\` and is written `\\` `\n` `\r` |
| `b2sum` | done — the six above plus `-l BITS` (a multiple of 8 up to 512, base 10) and the `BLAKE2b-NNN` tag, whose length a checksum line carries in base 0 — `BLAKE2b-020` is sixteen bits |
| `lib/digest.fern` | the seven checksum utilities, which are one program: the option surface, the escaping and the check-line grammar, parameterised by the digest |
| `lib/gnu.fern` | the GNU conventions every utility shares |
| `lib/base.fern` | the one encoder / decoder `base64`, `base32` and `basenc` drive, parameterised by alphabet, block and padding |
| `lib/cond.fern` | the conditional expression `test` and `[` evaluate |
| `lib/bre.fern` | regular expressions as glibc compiles them: POSIX basic for `expr`, syntax 0 (Emacs) for `tac -r` |
| `lib/ld.fern` | C's `long double` at the TARGET's format — strtold, arithmetic, rounding and the `%f` `%e` `%g` `%a` conversions — shared by `printf`, `numfmt`, `seq` and `sleep` |

The tracking epic (#8278) lists every other utility and its status.
