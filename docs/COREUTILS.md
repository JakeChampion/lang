# Fern coreutils

`coreutils/` reimplements GNU coreutils in Fern, one program per file, to two
requirements that do not bend:

1. **Byte-for-byte parity with GNU coreutils.** Same stdout, same stderr,
   same exit status, for every invocation. Not "compatible", not "the common
   cases": a divergence is a bug with the same standing as a miscompile.
2. **Faster than GNU on every utility, and faster than uutils (the Rust
   coreutils) wherever the language allows.** The point of doing this in
   Fern at all is to measure Fern against the two implementations people
   actually run, on the workloads people actually run them for.

The tracking epic is #8278; each utility has a sub-issue there. This document
is the standing definition the epic and the sub-issues point at.

## Why these programs

They are the best-specified CLI programs in existence: forty years of POSIX
text, a maintained reference implementation, and a second independent
implementation (uutils) that has already walked every quirk. That makes them
an unusually honest benchmark for a language: there is no room to define the
task around what the language finds easy. Every awkward corner — `echo`'s
octal escapes, getopt's unique-prefix matching, `false --help` exiting 1 from
stdout — is fixed in advance by a binary we can run.

They are also the workload Fern grew up on (small fast-startup CLI tools),
and the corpus covers the whole span from startup-bound (`true`, `echo`) to
throughput-bound (`yes`, `cat`) to compute-bound (`sort`, `sha256sum`) to
syscall-bound (`ls`, `du`).

## What parity means, exactly

For every case in the corpus, run under the environment below with argv[0]
set to the utility name, the Fern binary and the GNU binary produce:

- identical stdout bytes;
- identical stderr bytes;
- the same exit status, or death by the same signal (`yes | head` dies of
  SIGPIPE on both sides, and the harness checks that it does);
- for utilities that touch the filesystem, the same resulting tree.

The environment is `LC_ALL=C LANG=C TZ=UTC PATH=/usr/bin:/bin`, plus whatever
a case adds (`POSIXLY_CORRECT=1`). The C locale is a deliberate choice, not a
simplification: GNU's quoting, collation, case folding and number formatting
are all locale-dependent, and a case that passes in one locale and fails in
another proves nothing. C is the locale POSIX pins.

**argv[0] is set to the bare name on both sides.** GNU prints argv[0]
verbatim — directories and all — in every diagnostic and in the `Try 'yes
--help'` line, so two binaries at different paths differ on every error case
unless the harness equalises this. It does, and `coreutils/lib/gnu.fern`
reproduces the verbatim rule, so `/usr/local/bin/yes -x` says
`/usr/local/bin/yes: invalid option -- 'x'` exactly as GNU would.

### The two exemptions

Two outputs are ours by design, because their content names the
implementation:

- `--version` prints `<util> (Fern coreutils) <version>` and nothing else.
  Claiming GNU's version string would make the one output that identifies
  the program lie about which program it is.
- `--help` is our own text. GNU's is GPL-licensed prose carrying GNU's URLs,
  authors and (in 9.x) terminal hyperlink escapes; reproducing it would be
  copying, and it would be wrong in every particular that matters.

Exempt is not unchecked. `requireHelp` / `requireVersion` in the harness
still require the exit status and the stream to match GNU's for each — so
`false --help` writes to stdout and exits 1 in both — that stderr is empty on
both sides, that our first line is `Usage: <util>` / `<util> (Fern coreutils)`,
and that the output ends in a newline. The *behaviour* of the two options is
in the byte-exact corpus wherever it is observable without their text:
`--help=x`, `--hel`, `--help extra`, `--vers`, an operand before `--help`.

### What is not exempt

Everything else. In particular the things an implementer is tempted to call
cosmetic and are not:

- glibc's getopt messages, word for word: `invalid option -- 'x'`,
  `unrecognized option '--foo=bar'` (the whole token), `option '--lines'
  requires an argument` (the CANONICAL name, not the abbreviation typed),
  `option '--quiet' doesn't allow an argument`, `option '--v' is ambiguous;
  possibilities: '--verbose' '--version'` (declaration order, the whole
  token including any `=value`). A hidden option is visible here: `head`'s
  ambiguity list names `---presume-input-pipe`.
- strerror text: `No such file or directory`, `Is a directory`, `Bad file
  descriptor`, `No space left on device` — `IoError.Other` carries glibc's
  text for the errno on every backend (`internal/strerror`), so a write or
  open failure prints what C prints.
- The `Try '<argv0> --help' for more information.` line and the fact that a
  usage error never prints the full help.
- Per-utility exit codes for usage errors: 1 for most, 2 for `sort`, `expr`,
  `test`, … Each utility's sub-issue records its number.

## Nothing is copied from GNU

The implementations are written from the documented behaviour and from
running the reference binary. No GNU source is consulted for code, and no
GNU text is reproduced except the diagnostics that parity requires, which are
functional output, not prose. Where the same synopsis line appears
(`Usage: yes [STRING]...`) it is because there is one correct way to write
it.

## How parity is enforced

`internal/coreutils/` is the gate. It is oracle-based: no expected output is
ever written down. Each case is an invocation (argv, stdin, extra env, where
stdout goes — captured, closed, or `/dev/full` — and for a utility that never
stops, a byte limit); the harness runs GNU and Fern and diffs. A case costs one line, and a case cannot record a wrong
expectation, which is what makes the corpus cheap to grow and hard to get
wrong. See the package doc in `harness_test.go`.

The reference is whatever GNU coreutils the harness finds:
`$FERN_GNU_COREUTILS`, then the `yes` on PATH if its `--version` says GNU,
then the fixed system paths, then a nix store glob. **Not finding one is a
failure, not a skip.** On the Ubuntu CI runners it is the system coreutils;
on macOS the system tools are BSD, so a nix or Homebrew GNU coreutils is
needed and the failure message says so.

Versions: the corpus is held to GNU coreutils **9.x**. Local development
measured against 9.10; the Ubuntu runners carry the 9.x their release ships.
A case whose behaviour changed within 9.x records the version it needs in a
comment and is the exception, not the pattern — the utilities done so far
have no such case.

The package is in the unit-test lane (`scripts/unit-test-packages` derives
the lane from `go list`, so it was covered the moment it existed). It
compiles each utility once per process, without `-O` so the assert() checks
stay live. When the corpus grows past what the unit lane should carry, it
moves to a lane of its own; that is a workflow change, not a change here.

## Layout

```
coreutils/
  README.md         build / run / test / bench, for a reader who wants a binary
  lib/gnu.fern      what every utility shares with GNU: argv[0] verbatim,
                    the usage-error path, --help/--version handling,
                    strerror text, checked stdout writes
  lib/ld.fern       C's `long double` as the TARGET has it, for the
                    utilities that convert and compute in one
                    (printf, numfmt, seq, sleep)
  <util>.fern       one program per utility
internal/coreutils/
  harness_test.go   the oracle harness (this file's "How parity is enforced")
  longdouble_test.go
                    the long double each target gets, which the
                    host-oracle corpus cannot see (#8513)
  <util>_test.go    that utility's cases
scripts/coreutils-bench
                    hyperfine: Fern vs GNU vs uutils, one table
```

A utility is one file. Shared behaviour goes in `lib/` only once a
second utility needs it — the standard-options-only parse arrived with `yes`,
the sole-argument `--help` rule with `true`/`false`/`echo`, and the full
getopt_long emulation (valued options, permutation, `-n5` / `-n 5`, the
ambiguity list) arrives with the first utility that declares an option of its
own, as its own sub-issue. Do not build it ahead of a consumer.

`lib/ld.fern` is the one module that is not about GNU's conventions but
about the machine: `printf`, `numfmt` and `seq` all compute in C's `long
double`, which is x87 80-bit extended on x86-64, IEEE binary128 on arm64
and wasm32, and plain binary64 on Darwin. It is a `Format` — significand
bits, exponent range, whether the leading bit is stored — plus parsing
(`strtold`), arithmetic (add / sub / mul / div / compare, each rounded
once), the exact decimal expansion, the `%a` / `%e` / `%f` / `%g` bodies,
and the facts a utility reads off the format rather than the value:
`LDBL_DIG`, the largest exactly-held integer, the `--round` modes.
`format()` reads `target_arch()` / `target_os()`, so those fold before
the checker and a build carries one model.

**A host oracle can only ever prove the format it runs on.** That is how
the hardcoded x87 model in #8513 survived a year, and it is why there is
one module rather than one per utility: a second copy is a second place
for the same bug, and numfmt shipped exactly that — its own 64-bit
significand, 29 of its 662 cases diverging on aarch64 with nothing on an
x86-64 host to show it. Two gates cover what the corpus cannot see:
`internal/coreutils/longdouble_test.go` checks every target's selection
and FAILS rather than guess when a target it does not know appears, and
`examples/tests/coreutils_ld_test.fern` drives all three formats
explicitly on whatever host runs it. A utility that converts in one also
gets a block of cases holding the invocations whose bytes DIFFER between
the three formats — printf's and seq's are marked as such — so the leg
that does run proves something about the choice rather than only about
the arithmetic.

CI runs the corpus on both formats because the unit lane's matrix has an
`ubuntu-24.04-arm` runner. **From an x86-64 desk the other leg is a
cross run**: compile the utilities for aarch64 and put both sides under
qemu, against a real aarch64 GNU build.

```
apt-get download coreutils:arm64 && dpkg-deb -x coreutils_*_arm64.deb /tmp/gnu-arm64
FERN_COREUTILS_TARGET=arm64-linux \
FERN_COREUTILS_QEMU="qemu-aarch64 -L /usr/aarch64-linux-gnu" \
FERN_GNU_COREUTILS=/tmp/gnu-arm64/usr/bin \
  go test ./internal/coreutils/ -run TestSeq -p 1
```

The sysroot is for the dynamically linked GNU binaries; the Fern ones are
static, and a GNU binary whose libraries are missing dies with exit 127
rather than diverging quietly (`expr` and `factor` want `libgmp10:arm64`
in that sysroot too). This is a debug affordance for the #8513 class of
bug and not a gate — under qemu the corpus runs an order of magnitude
slower, and CI runs the same cases natively. Select the utility you are
working on: printf's two cases that make GNU build a two-gigabyte field
cost seconds natively and many minutes emulated.

**Anything a utility reads off `long double` belongs in this module**,
not just arithmetic. `MAX_UNSCALED_DIGITS` in numfmt is GNU's `LDBL_DIG`
— 18, 33 or 15 — so a value GNU prints on one machine it refuses on
another; a literal 18 there was the same bug wearing different clothes.
seq carried the same 18 as the precision its scaled-decimal engine would
run, and the assumption underneath its i64 scaling — that the format
holds every 64-bit integer exactly — is true of x87 and binary128 and
false of binary64. Both come off the `Format` now (`dig()`,
`max_exact_u64()`). Note what that costs to find: the binary64 half of it
is invisible to every gate here, because no lane runs the corpus on
Darwin.

## Adding a utility

1. Read its sub-issue for the recorded quirks and its usage-error exit code.
2. **Probe the reference before writing a line.** Run the GNU binary on every
   edge you can think of and record what it does; the cases in
   `internal/coreutils/<util>_test.go` are that probe made permanent. The
   `echo` octal rule (`\NNN` as well as `\0NNN`, both wrapping at a byte)
   and `yes`'s permuting option scan were both found this way after the
   first implementation had them wrong.
3. Write `coreutils/<util>.fern`. Use `lib/gnu.fern` for everything it
   already covers. Output goes through a held `Writer` in one write per block,
   never a `print` per line: the first `yes` measured 2.8 MB/s that way
   against GNU's 385 MB/s, and the entire gap was the syscall per line.
4. Cases: every option, every option combination that changes behaviour,
   every error path, `--`, `-`, an empty operand, an operand that is not
   valid UTF-8, POSIXLY_CORRECT if the utility reads it, and the write-failure
   paths once #8265 lands. Run the gate; iterate until it is green.
5. Add the utility's workloads to `scripts/coreutils-bench` and record its
   first numbers in the sub-issue. If it is slower than GNU, that is the
   next task, not a footnote.
6. Any Fern quirk or bug you hit on the way gets an issue and a fix, never a
   workaround. That is the project's standing order and it is doubly so here,
   where the whole exercise is to find them.

## Performance

`scripts/coreutils-bench` compiles the utilities with `-O` for the host and
runs each workload under hyperfine for Fern, GNU and (when present) uutils,
with the same command shape for all three and any pipeline partner taken from
the GNU directory so it is a constant. Wall time, mean ± σ, ≥20 runs; only
comparable within one run on one machine.

Baseline, first four utilities, 2026-09-05, each bench run alone on its
machine. Ratios above 1 mean Fern is faster.

Linux arm64 (Debian container on Apple M-series; GNU coreutils 9.1; no
uutils in the image):

| utility | workload | fern (ms) | gnu (ms) | gnu / fern |
|---|---|---|---|---|
| `echo` | echo hello world | 0.15 ± 0.23 | 0.20 ± 0.29 | 1.32× |
| `echo` | echo -e with escapes | 0.15 ± 0.28 | 0.20 ± 0.25 | 1.34× |
| `echo` | echo 200 operands | 0.17 ± 0.22 | 0.21 ± 0.26 | 1.27× |
| `false` | false | 0.14 ± 0.19 | 0.19 ± 0.17 | 1.35× |
| `true` | true | 0.15 ± 0.17 | 0.19 ± 0.15 | 1.28× |
| `yes` | yes | head -c 1G | 158.50 ± 41.23 | 168.00 ± 25.85 | 1.06× |
| `yes` | yes 70000-byte line | head -c 1G | 223.02 ± 14.34 | 234.68 ± 35.67 | 1.05× |

macOS arm64 (Apple M-series; GNU coreutils 9.10; uutils 0.6.0):

| utility | workload | fern (ms) | gnu (ms) | uutils (ms) | gnu / fern | uutils / fern |
|---|---|---|---|---|---|---|
| `echo` | echo hello world | 1.77 ± 7.23 | 3.18 ± 12.93 | 4.32 ± 13.49 | 1.79× | 2.43× |
| `echo` | echo -e with escapes | 1.78 ± 8.71 | 3.24 ± 13.25 | 4.38 ± 13.49 | 1.82× | 2.47× |
| `echo` | echo 200 operands | 1.87 ± 10.07 | 3.26 ± 13.49 | 4.13 ± 11.80 | 1.74× | 2.21× |
| `false` | false | 1.65 ± 7.18 | 2.88 ± 12.25 | 5.99 ± 16.42 | 1.74× | 3.63× |
| `true` | true | 1.88 ± 7.60 | 2.81 ± 10.55 | 4.66 ± 17.36 | 1.49× | 2.47× |
| `yes` | yes | head -c 1G | 674.49 ± 38.15 | 507.69 ± 42.98 | 687.22 ± 106.51 | 0.75× | 1.02× |
| `yes` | yes 70000-byte line | head -c 1G | 590.85 ± 40.52 | 641.59 ± 121.13 | 658.07 ± 114.40 | 1.09× | 1.11× |

Reading it: `true`, `false` and `echo` are startup-bound, and a Fern binary
is a static executable with no dynamic loader and no libc initialisation —
that is the whole margin, and it is larger on macOS where the loader costs
more. `yes` is pipe-bound and its number is the write block size, which was
measured, not chosen: a C `write(2)` loop through the same pipe puts the
optimum at 4 KiB on Linux (138 ms median; GNU's 8 KiB, 163) and 1 KiB on
macOS (482 ms; GNU writes 1 KiB there, 477), with every size from 2 KiB up
costing 570–680 ms on macOS because a write that overfills the 64 KiB pipe
puts writer and reader into lockstep. `yes.fern` selects the block with
`target_os()` — 4 KiB compiled for Linux, 1 KiB for macOS — so it is 1.06×
GNU on Linux and writes the same 1 KiB GNU does on macOS. The sweeps are
recorded in `yes.fern`.

## Open gaps

None. Both Fern gaps the first utilities met — `IoError.Other` carrying no
strerror text (#8265) and source unable to learn its compile target
(#8338) — are closed, and each is exercised by the corpus: the
write-failure cases (`yes >&-`, `> /dev/full`) and `yes.fern`'s per-target
block. A gap met later gets an issue and a fix, never a corpus carve-out.

## Staging

Utilities are grouped by what they need from the Fern runtime, and the
groups are the order of work. Each sub-issue names its group.

- **A. argv and stdout only** — `true` `false` `yes` `echo` (done), `printf`
  `basename` `dirname` `seq` `expr` `factor` `numfmt` `test` `[` `tsort`
  `sleep`. No new runtime surface; the full getopt emulation lands here.
- **B. streaming text** — `cat` `tac` `head` `tail` `wc` `nl` `cut` `paste`
  `join` `comm` `uniq` `sort` `tr` `fold` `fmt` `pr` `ptx` `expand`
  `unexpand` `split` `csplit` `shuf` `od` `base32` `base64` `basenc` `cksum`
  `sum` `md5sum` `sha1sum` `sha224sum` `sha256sum` `sha384sum` `sha512sum`
  `b2sum` `tee`. Needs a buffered stdout writer in `std/io_buffered` (its
  own header already promises one) and a streaming stdin reader. The hash
  utilities have their digests: `std/crypto` streams MD5, SHA-1,
  SHA-224/256/384/512 and BLAKE2b (`h = h.update(chunk)` per `read_chunk`
  piece), and `std/hash` has cksum's CRC-32 and both sum(1) checksums with
  their block counts. `tail -f` waits for group C.
- **C. needs a runtime primitive first** — everything that reads the process
  or the filesystem beyond `read_file` / `stat` / `read_dir`: `pwd`
  (getcwd), `tty` (ttyname), `nproc` (affinity), `uname` `arch` (uname),
  `whoami` `id` `groups` `logname` `users` `who` `pinky` (uid, passwd,
  utmp), `printenv` `env` (the whole environ, exec), `link` `ln` `readlink`
  `realpath` (link, symlink, readlink), `mkdir` `rmdir` `rm` `mv` `cp`
  `install` `touch` `truncate` `mkfifo` `mknod` `mktemp` `sync` (mkdir with
  mode, rmdir, rename, utimensat, ftruncate, mknod, fsync), `chmod` `chown`
  `chgrp` `chcon` `runcon`, `stat` `ls` `dir` `vdir` `du` `df` `dircolors`
  (full stat, statfs, d_type), `date` (strftime, timezone), `timeout` `nice`
  `nohup` `kill` `stdbuf` `chroot` (signals, setpriority, exec), `dd`
  `shred` `stty` `uptime` `hostid` `pathchk`. Each primitive is a builtin,
  which is four classifications (`docs/FREESTANDING-CORE.md`,
  `docs/PACKAGE-CAPABILITIES-BRIEF.md`) and the self-host mirror. The
  sub-issue for each utility names the primitives it is blocked on; the
  primitive gets its own issue when the first utility needs it.

Within a group, easiest first. Do not start a group-C utility by adding a
one-off syscall to one backend.
