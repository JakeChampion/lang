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
stdout goes — captured, closed, or `/dev/full` — for a utility that never
stops, a byte limit, and for `test -t`, a pseudo-terminal on fd 3); the
harness runs GNU and Fern and diffs. A case costs one line, and a case cannot
record a wrong expectation, which is what makes the corpus cheap to grow and
hard to get wrong. See the package doc in `harness_test.go`.

A utility that reads the filesystem is asked about a tree its corpus builds
under `t.TempDir()` — `test`'s has every file kind it can tell apart, the
three special bits, pinned timestamps that differ below the second, a hard
link, and a block device (made with mknod when the process may, else one
the system has). Both sides see the same tree, so the answer on a machine
where the suite runs as root (`-r` on a mode-0 file is true there) is still
the same answer on both.

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

### The self-host leg

`TestSelfHostCoreutilsParity` compiles every utility a second time with the
SELF-HOST compiler (`examples/self_host/fern.fern`) under `FERN_STRICT_IR=1`,
runs the same corpus against those binaries, and requires them to agree with
the native build. Comparing against native rather than GNU is deliberate:
native is already held to GNU by the corpus above, so a failure here says the
two COMPILERS disagree instead of re-reporting a parity bug in both.

It exists because nothing else compiles this tree with the self-host compiler,
and the gap that hid behind that was not small: the getopt cursor returns
`(Option[OptMatch], Getopt)`, which the self-host tuple lowering refused, so
every utility declaring an option bailed the module (#8407). The first green
run of the leg then found `Writer.close()` answering None to a failing close
on all three self-host backends, which is the whole of `close_stdout`'s
decision (#8569). `TestSelfHostCoreutilsCoverage` fails when a utility has no
entry in `corpusByUtil`, so a new one cannot join the tree without joining
this leg.

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
  lib/digest.fern   md5sum, sha1sum, sha224sum, sha256sum, sha384sum,
                    sha512sum and b2sum, which GNU also builds from one
                    source: the option surface, the file-name escaping
                    and the check-line grammar, parameterised by the
                    digest each of the seven names
  lib/resolv.fern   glibc's IPv4 name lookup — /etc/hosts, the
                    `hosts:` line of nsswitch.conf, resolv.conf and an
                    RFC 1035 A query — for the utilities that resolve
                    the machine's own name (hostid; hostname, uname
                    and who reach for the same pieces)
  <util>.fern       one program per utility
internal/coreutils/
  harness_test.go   the oracle harness (this file's "How parity is enforced")
  longdouble_test.go
                    the long double each target gets, which the
                    host-oracle corpus cannot see (#8513)
  <util>_test.go    that utility's cases
  sums_test.go      the corpus the seven checksum utilities share, since
                    they are one program: each <util>_test.go names its
                    own digest and calls it
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

Linux x86-64 (a 4-core dev container, 2026-09-06, with two other agents'
builds running on the same cores at the time — the σ is theirs; GNU
coreutils 9.4; uutils 0.0.24 as the Debian multi-call binary, which the
script now detects):

| utility | workload | fern (ms) | gnu (ms) | uutils (ms) | gnu / fern | uutils / fern |
|---|---|---|---|---|---|---|
| `hostid` | hostid | 0.23 ± 0.08 | 1.09 ± 0.13 | 1.93 ± 0.18 | 4.69× | 8.30× |

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
recorded in `yes.fern`. `hostid` is startup plus three file reads
(`/etc/hostid`, `/etc/nsswitch.conf`, `/etc/hosts`) and one uname(2); GNU
pays the dynamic loader and then dlopens the NSS modules named on the
`hosts:` line, which is the whole 4.7×, and uutils' multi-call dispatch
costs it another millisecond.

Group B's first two, 2026-09-06, Linux x86-64 (GNU coreutils 9.4; no
uutils in the image). The file is 62 MiB / 8 000 000 lines of `seq`:

| utility | workload | fern (ms) | gnu (ms) | gnu / fern |
|---|---|---|---|---|
| `head` | `-n 10` of a 62 MiB file | 0.48 ± 0.59 | 1.63 ± 0.82 | 3.42× |
| `head` | `-n 4000000` of a 62 MiB file | 7.84 ± 1.46 | 27.33 ± 2.32 | 3.48× |
| `head` | `-c 32M` of a 62 MiB file | 6.56 ± 0.57 | 12.40 ± 0.89 | 1.89× |
| `head` | `-n 10` from a pipe | 1.84 ± 0.56 | 1.85 ± 0.55 | 1.01× |
| `head` | `-n -10` of a 62 MiB file | 21.64 ± 1.26 | 21.05 ± 1.17 | 0.97× |
| `head` | `-c -10` of a 62 MiB file | 17.96 ± 1.69 | 20.29 ± 0.71 | 1.13× |
| `wc` | `-l` of a 62 MiB file | 17.06 ± 1.01 | 17.09 ± 0.90 | 1.00× |
| `wc` | `-c` of a 62 MiB file | 0.34 ± 0.31 | 1.53 ± 0.44 | 4.47× |
| `wc` | (default) of a 62 MiB file | 356.59 ± 9.72 | 1465.41 ± 17.49 | 4.11× |
| `wc` | `-w` of a 62 MiB file | 350.04 ± 2.92 | 1467.80 ± 15.79 | 4.19× |
| `wc` | `-L` of a 62 MiB file | 351.76 ± 3.67 | 1471.15 ± 14.96 | 4.18× |
| `wc` | `-l` from a pipe | 65.26 ± 30.37 | 68.91 ± 13.86 | 1.06× |

Reading THAT one: the 4× rows are the per-byte scan, where GNU spends 1.4 s of
user time on 62 MiB and Fern spends 0.34; `wc -c` never reads at all, taking
the size off `stat` as GNU does. The two rows that only tie are the ones that
are already at memory bandwidth — `wc -l` is one `__count_byte` per read and
nothing else, and the last 25% of ITS user time is the SSE2 kernel's 16 bytes
an iteration against glibc's AVX2 32 (#8716).

The counting shapes were where the first draft lost: `head -n 4000000` called
`__memchr` once per line (0.73×) and `head -n -10` rebuilt its withheld tail
per chunk (0.17×). Both now decide a whole chunk with one `__count_byte` and
walk only the chunk that reaches the count — backwards, with `__rmemchr`, for
the elision.

The seven checksum utilities, 2026-09-06, Linux x86-64 (GNU coreutils 9.4;
uutils 0.0.24 as the Debian multi-call binary; another agent's bench was
running on the same four cores, which is where the wider σ comes from). The
file is the same 62 MiB / 8 000 000 lines of `seq`; the `-c` workload is 500
small files and a checksum file over them:

| utility | workload | fern (ms) | gnu (ms) | uutils (ms) | gnu / fern | uutils / fern |
|---|---|---|---|---|---|---|
| `md5sum` | md5sum of a 62 MiB file | 277.06 ± 14.26 | 109.65 ± 4.99 | 134.92 ± 3.19 | 0.40× | 0.49× |
| `md5sum` | md5sum of a 62 MiB file from a pipe | 296.77 ± 8.97 | 122.05 ± 15.39 | 167.54 ± 6.43 | 0.41× | 0.56× |
| `md5sum` | md5sum --tag of a 62 MiB file | 275.53 ± 17.96 | 113.33 ± 7.08 | 133.32 ± 8.48 | 0.41× | 0.48× |
| `md5sum` | md5sum of a small file | 0.72 ± 1.73 | 2.26 ± 3.71 | 2.93 ± 5.50 | 3.14× | 4.06× |
| `md5sum` | md5sum -c over 500 small files | 8.04 ± 5.92 | 4.76 ± 3.54 | 8.38 ± 10.75 | 0.59× | 1.04× |
| `sha1sum` | sha1sum of a 62 MiB file | 350.96 ± 11.23 | 58.62 ± 6.57 | 59.02 ± 5.32 | 0.17× | 0.17× |
| `sha1sum` | sha1sum of a 62 MiB file from a pipe | 326.49 ± 37.23 | 65.12 ± 10.57 | 72.74 ± 16.20 | 0.20× | 0.22× |
| `sha1sum` | sha1sum --tag of a 62 MiB file | 347.41 ± 5.22 | 52.79 ± 0.65 | 53.99 ± 1.50 | 0.15× | 0.16× |
| `sha1sum` | sha1sum of a small file | 0.41 ± 0.95 | 1.29 ± 0.23 | 1.76 ± 0.24 | 3.12× | 4.26× |
| `sha1sum` | sha1sum -c over 500 small files | 6.19 ± 0.28 | 3.12 ± 0.77 | 6.99 ± 3.74 | 0.50× | 1.13× |
| `sha224sum` | sha224sum of a 62 MiB file | 868.40 ± 26.35 | 57.82 ± 2.89 | 58.73 ± 1.78 | 0.07× | 0.07× |
| `sha224sum` | sha224sum of a 62 MiB file from a pipe | 870.77 ± 24.83 | 70.71 ± 6.53 | 73.89 ± 3.18 | 0.08× | 0.08× |
| `sha224sum` | sha224sum --tag of a 62 MiB file | 880.20 ± 80.42 | 65.11 ± 12.66 | 59.47 ± 3.64 | 0.07× | 0.07× |
| `sha224sum` | sha224sum of a small file | 0.23 ± 0.41 | 1.54 ± 1.07 | 1.81 ± 0.37 | 6.76× | 7.97× |
| `sha224sum` | sha224sum -c over 500 small files | 11.17 ± 4.00 | 3.86 ± 1.54 | 5.11 ± 1.65 | 0.35× | 0.46× |
| `sha256sum` | sha256sum of a 62 MiB file | 857.73 ± 32.55 | 58.12 ± 3.47 | 60.13 ± 1.94 | 0.07× | 0.07× |
| `sha256sum` | sha256sum of a 62 MiB file from a pipe | 873.83 ± 35.99 | 68.72 ± 3.41 | 73.62 ± 2.48 | 0.08× | 0.08× |
| `sha256sum` | sha256sum --tag of a 62 MiB file | 874.49 ± 33.83 | 62.48 ± 10.84 | 77.93 ± 26.57 | 0.07× | 0.09× |
| `sha256sum` | sha256sum of a small file | 1.07 ± 5.99 | 3.08 ± 11.71 | 3.18 ± 4.27 | 2.87× | 2.97× |
| `sha256sum` | sha256sum -c over 500 small files | 12.91 ± 5.82 | 10.07 ± 6.91 | 5.21 ± 3.90 | 0.78× | 0.40× |
| `sha384sum` | sha384sum of a 62 MiB file | 661.23 ± 10.23 | 108.96 ± 3.01 | 122.23 ± 2.13 | 0.16× | 0.18× |
| `sha384sum` | sha384sum of a 62 MiB file from a pipe | 682.66 ± 24.87 | 122.20 ± 5.24 | 154.22 ± 8.52 | 0.18× | 0.23× |
| `sha384sum` | sha384sum --tag of a 62 MiB file | 655.54 ± 11.79 | 110.49 ± 3.00 | 124.16 ± 1.87 | 0.17× | 0.19× |
| `sha384sum` | sha384sum of a small file | 0.37 ± 0.55 | 1.66 ± 0.54 | 2.14 ± 0.16 | 4.53× | 5.86× |
| `sha384sum` | sha384sum -c over 500 small files | 9.89 ± 0.42 | 3.86 ± 0.27 | 5.73 ± 1.50 | 0.39× | 0.58× |
| `sha512sum` | sha512sum of a 62 MiB file | 661.45 ± 33.61 | 113.48 ± 4.06 | 126.56 ± 4.89 | 0.17× | 0.19× |
| `sha512sum` | sha512sum of a 62 MiB file from a pipe | 647.83 ± 22.83 | 119.03 ± 4.46 | 140.91 ± 9.91 | 0.18× | 0.22× |
| `sha512sum` | sha512sum --tag of a 62 MiB file | 653.46 ± 37.41 | 111.69 ± 3.47 | 124.64 ± 3.87 | 0.17× | 0.19× |
| `sha512sum` | sha512sum of a small file | 1.15 ± 2.11 | 2.05 ± 1.17 | 3.25 ± 1.44 | 1.79× | 2.83× |
| `sha512sum` | sha512sum -c over 500 small files | 10.22 ± 0.46 | 3.56 ± 0.76 | 7.38 ± 3.96 | 0.35× | 0.72× |
| `b2sum` | b2sum of a 62 MiB file | 392.74 ± 26.77 | 105.44 ± 18.06 | 84.49 ± 8.34 | 0.27× | 0.22× |
| `b2sum` | b2sum of a 62 MiB file from a pipe | 431.94 ± 33.24 | 104.01 ± 3.18 | 89.75 ± 2.73 | 0.24× | 0.21× |
| `b2sum` | b2sum --tag of a 62 MiB file | 411.74 ± 13.36 | 93.01 ± 3.14 | 78.54 ± 7.34 | 0.23× | 0.19× |
| `b2sum` | b2sum of a small file | 0.31 ± 0.71 | 1.02 ± 0.63 | 1.89 ± 0.41 | 3.34× | 6.17× |
| `b2sum` | b2sum -c over 500 small files | 9.13 ± 2.24 | 4.07 ± 2.19 | 5.05 ± 0.70 | 0.45× | 0.55× |
| `b2sum` | b2sum -l 256 of a 62 MiB file | 376.23 ± 23.80 | 91.98 ± 1.98 | 78.72 ± 3.30 | 0.24× | 0.21× |

Reading the checksum table: **Fern loses every throughput row and wins
every startup row, and the whole of both is one thing — these programs are
compute-bound on the digest kernel.** The driver is not in it: raising the
read block from 64 KiB to 256 KiB moves `md5sum` by less than the
run-to-run noise.

Two different comparisons are stacked in that table and they are worth
separating. Debian's `md5sum` and the five `sha*sum` binaries link
`libcrypto.so.3`, so those rows put Fern against OpenSSL's hand-written
assembly — and on this host `sha256sum` is running SHA-NI, a hardware
instruction, which is the 0.07× and is not a codegen comparison at all.
uutils reaches the same instruction through the `sha2` crate, hence its
identical numbers. **`b2sum` is the row that carries information**: GNU's
links no libcrypto and is plain portable C, and Fern is 4× off it at the
identical algorithm.

`internal/stdlib/std/crypto.fern`'s kernels are where that goes, and #8782
takes it apart: `__blake2b_blocks` compiles to 10 160 instructions where a C
compiler needs about 1 150, 5 139 of them `mov`, because the x86-64 emitter
is a stack machine and every one of the 32 hot 64-bit locals is a stack slot.
Three smaller causes sit behind it — a rotate lowers to shl/shr/or with not
one `rol` in the function, the xor under each rotate is written twice and not
CSEd, and a little-endian word load is eight bounds-checked byte loads. None
of them is specific to hashing.

The startup rows are the same static-binary margin `true` and `echo` measure,
widened: GNU pays the dynamic loader AND `dlopen`s libcrypto before it hashes
a hundred bytes.

## Known divergences

**`hostid` asks DNS over TCP.** The id is glibc's `gethostid`: `/etc/hostid`
if it holds four bytes, else the hostname's IPv4 address with its halves
swapped, else 0 — and the address comes from NSS, which `lib/resolv.fern`
reimplements: the `hosts:` line of `/etc/nsswitch.conf` with its bracketed
actions, `/etc/hosts` as the `files` backend reads it, and `/etc/resolv.conf`
with res_search's search-list order. The DNS leg is where it parts from
glibc, in one way: glibc asks over UDP and retries over TCP only on a
truncated reply, while this resolver asks over TCP from the start, because
Fern has no UDP receive. RFC 1035 obliges every nameserver to answer the
same query over TCP, so the A records — and the id — are the same; a
nameserver that refuses TCP altogether is the one host where GNU prints an
address-derived id and this prints `00000000`. UDP is deliberately NOT
added for this: one utility's resolver is not the reason to grow the
runtime's socket surface, and the record here is what keeps that decision
visible. Two smaller edges in the same leg: only the `files` and `dns`
sources are implemented (any other, `myhostname` included, reports UNAVAIL
as a source with no module does, so a name in neither file nor DNS prints
`00000000` where nss-myhostname would answer 127.0.0.2), and a nameserver
that black-holes the connection holds `hostid` for the kernel's connect
timeout where glibc gives up after resolv.conf's `timeout` × `attempts`.
Neither changes the bytes on a host whose name resolves.

## Open gaps

**fstat(2) on a descriptor (#8713).** `stat(path)` is the only way into a
`struct stat`, and two behaviours ask the same question of a DESCRIPTOR
instead. `wc` fixes its column width from the sizes of its operands, taking
GNU's `fstat(STDIN_FILENO)` for a `-` or absent one, so `wc < f` pads to the
file's digits where `wc.fern` pads to seven; the single-count form is
unaffected because GNU skips the stat there too. `cat` fstats fd 1 before it
reads anything and dies `cat: standard output: Bad file descriptor` when that
fails, which is unreachable by writing because it has nothing to write —
`cat` is not implementable to parity until the primitive exists.

Neither is visible to this corpus: the harness always hands the child a pipe
for stdin, so the width is 7 on both sides, and `cat` is not in the tree. That
is what makes them worth writing down here rather than leaving to a gate.

None. Both Fern gaps the first utilities met — `IoError.Other` carrying no
strerror text (#8265) and source unable to learn its compile target
(#8338) — are closed, and each is exercised by the corpus: the
write-failure cases (`yes >&-`, `> /dev/full`) and `yes.fern`'s per-target
block. `hostid` needed a runtime primitive rather than a fix — `hostname()`,
gethostname(2) on every backend (#8529) — and got it under its own
capability rather than a one-off syscall on one backend. A gap met later
gets an issue and a fix, never a corpus carve-out.

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
  `b2sum` `tee`. `head`, `wc` and the seven checksum utilities are done. Needs a buffered stdout writer in
  `std/io_buffered` (its own header already promises one) and a streaming
  stdin reader whose reads can FAIL: every one of these reaches a read error
  through a directory operand, and `Reader.read_chunk` answered None to EOF
  and to EISDIR alike until #8700 gave it `Result[string, IoError]`. The hash
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
  `shred` `stty` `uptime` `pathchk`, and `hostid` (done: `hostname()`
  plus the resolver in `lib/resolv.fern`). Each primitive is a builtin,
  which is four classifications (`docs/FREESTANDING-CORE.md`,
  `docs/PACKAGE-CAPABILITIES-BRIEF.md`) and the self-host mirror. The
  sub-issue for each utility names the primitives it is blocked on; the
  primitive gets its own issue when the first utility needs it.

Within a group, easiest first. Do not start a group-C utility by adding a
one-off syscall to one backend.
