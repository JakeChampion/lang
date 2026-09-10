# Fern coreutils

`coreutils/` reimplements GNU coreutils in Fern, one program per file, to two
requirements that do not bend:

1. **Byte-for-byte parity with GNU coreutils 9.4 or newer.** Same stdout, same stderr,
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

A case may also ask for a working directory of its own — a fresh one per
SIDE, seeded by a function it names — and the TREE it leaves behind is then
compared alongside the streams, name by name and byte by byte. `split` is
what needs it: its whole output is the files it writes, so the streams alone
would compare two silences.

A utility that reads the filesystem is asked about a tree its corpus builds
under `t.TempDir()` — `test`'s has every file kind it can tell apart, the
three special bits, pinned timestamps that differ below the second, a hard
link, and a block device (made with mknod when the process may, else one
the system has). Both sides see the same tree, so the answer on a machine
where the suite runs as root (`-r` on a mode-0 file is true there) is still
the same answer on both.

A utility that MUTATES the filesystem needs the other shape, and a case gets it
by naming a `tree` builder: a fresh empty directory PER SIDE, populated by that
function, with the child's cwd set to it and its operands written relative to
it. After both runs the two directories are compared entry for entry — kind,
the twelve-bit mode, a symlink's target, a file's size and contents, and which
names share an inode, rendered as link-group numbers so the equivalence is
compared and the inodes themselves are not. That last field is what separates
`ln a b` from `cp a b`, and it is the only thing `link`'s corpus has to compare
at all: the utility writes nothing on stdout.

Two directories rather than one is the point. The older `dir` / `prepare` /
`artifacts` trio hands both sides one shared directory and compares named
files' bytes, which is right for a utility whose output is a file it was told
to write (`uniq f out`) and wrong for one whose answer is which entries exist.

The reference is whatever GNU coreutils the harness finds:
`$FERN_GNU_COREUTILS`, then the `yes` on PATH if its `--version` says GNU,
then the fixed system paths, then a nix store glob. **Not finding one is a
failure, not a skip.** On the Ubuntu CI runners it is the system coreutils;
on macOS the system tools are BSD, so a nix or Homebrew GNU coreutils is
needed and the failure message says so.

Versions: the corpus is held to GNU coreutils **9.4 or newer**. Benchmarks
compare against both GNU coreutils and Rust uutils, recording their actual
versions. Install missing comparison implementations before measuring.
A case whose behaviour changed between versions records the version it needs in a
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
                    strerror text, checked stdout writes, and glibc's
                    stdio buffering for the utilities whose write-error
                    wording depends on it (tac)
  lib/bre.fern      regular expressions as glibc compiles them —
                    POSIX basic for expr, syntax 0 (Emacs) for tac -r,
                    anchored or searched over a range of a buffer for
                    nl and csplit, with a literal and a literal-prefix
                    fast path ahead of glibc's fastmap and the
                    simulation, and glibc's regerror texts as the
                    diagnostics
  lib/ld.fern       C's `long double` as the TARGET has it, for the
                    utilities that convert and compute in one
                    (printf, numfmt, seq, sleep)
  lib/base.fern     the encoder / decoder base64, base32 and basenc
                    share: one codec parameterised by alphabet, block
                    and padding, plus every decode rule and diagnostic
  lib/tabs.fern     the `-t` tab-stop grammar and lookup expand and
                    unexpand share
  lib/digest.fern   md5sum, sha1sum, sha224sum, sha256sum, sha384sum,
                    sha512sum and b2sum, which GNU also builds from one
                    source: the option surface, the file-name escaping
                    and the check-line grammar, parameterised by the
                    digest each of the seven names
  lib/pwdb.fern     /etc/passwd and /etc/group as glibc's `files`
                    backend reads them — the lookups by name and by id,
                    getgrouplist's ordering, and the process's own group
                    set — for whoami, id, groups and logname
  lib/utmp.fern     the login-accounting record: the fixed-size utmp
                    entry and the scans over it, for logname today and
                    users / who / pinky next
  lib/sys.fern      the five fields of the kernel's utsname record, by
                    name, for the utilities that print the record
                    (uname) or one field of it (arch)
  lib/resolv.fern   glibc's IPv4 name lookup — /etc/hosts, the
                    `hosts:` line of nsswitch.conf, resolv.conf and an
                    RFC 1035 A query — for the utilities that resolve
                    the machine's own name (hostid; hostname, uname
                    and who reach for the same pieces)
  <util>.fern       one program per utility
internal/coreutils/
  harness_test.go   the oracle harness (this file's "How parity is enforced"),
                    including the per-case working directory and resulting-tree
                    comparison a filesystem-MUTATING utility needs
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

Group B's next two, 2026-09-06, Linux x86-64, same 62 MiB file, GNU 9.4 and
uutils 0.0.24 both present:

| utility | workload | fern (ms) | gnu (ms) | uutils (ms) | gnu / fern | uutils / fern |
|---|---|---|---|---|---|---|
| `cat` | a 62 MiB file | 7.11 ± 0.75 | 7.08 ± 0.47 | 3.62 ± 0.35 | 1.00× | 0.51× |
| `cat` | two 62 MiB files | 13.74 ± 0.96 | 12.77 ± 0.96 | 4.85 ± 0.48 | 0.93× | 0.35× |
| `cat` | from a pipe | 59.81 ± 39.78 | 52.78 ± 23.74 | 39.74 ± 5.65 | 0.88× | 0.66× |
| `cat` | `-n` of a 62 MiB file | 549.91 ± 46.46 | 122.45 ± 13.05 | 1223.18 ± 80.48 | 0.22× | 2.22× |
| `cat` | `-s` of a 62 MiB file | 68.04 ± 5.67 | 60.50 ± 4.60 | 1042.84 ± 79.02 | 0.89× | 15.33× |
| `cat` | `-A` of a 62 MiB file | 380.59 ± 36.87 | 74.33 ± 6.69 | 1527.29 ± 135.14 | 0.20× | 4.01× |
| `tail` | `-n 10` of a 62 MiB file | 0.26 ± 0.08 | 1.07 ± 0.13 | 2.07 ± 0.23 | 4.15× | 8.03× |
| `tail` | `-n 4000000` of a 62 MiB file | 40.12 ± 3.30 | 36.11 ± 3.30 | 20.04 ± 2.22 | 0.90× | 0.50× |
| `tail` | `-c 32M` of a 62 MiB file | 5.48 ± 0.59 | 6.37 ± 0.54 | 2.73 ± 0.28 | 1.16× | 0.50× |
| `tail` | `-n 10` from a pipe | 58.05 ± 3.02 | 110.10 ± 8.45 | 68.51 ± 37.19 | 1.90× | 1.18× |
| `tail` | `-n +4000000` of a 62 MiB file | 10.21 ± 1.26 | 33.96 ± 3.07 | 40.99 ± 2.86 | 3.32× | 4.01× |

Reading it: `tail` wins where the answer is a seek — `-n 10` reads one block
from the end instead of the file, and `-n +4000000` skips forward rather
than holding lines — and ties where it is a copy. `cat` ties on the copy and
LOSES on every formatting mode, and the modes step up in proportion to how
many appends they do rather than how many bytes they move: plain 7 ms, `-s`
68 (one append per run of lines), `-A` 381 (two per line plus a per-byte
table lookup), `-n` 550 (four per line). That is #8770 — a string append
costs 8-16 ns whatever its size — measured there rather than guessed, and it
is below the utility: two rewrites of `cat -n` around it each came out a
wash and were reverted. uutils loses the same shapes far worse.

The one row where BOTH lose to uutils is the plain copy, 3.6 ms against 7.1:
uutils reaches for `copy_file_range(2)` and moves the bytes without a
round trip through user space, where Fern and GNU both read and write.

`csplit`, 2026-09-07, Linux x86-64, same 62 MiB file, GNU 9.4 and uutils
0.0.24:

| utility | workload | fern (ms) | gnu (ms) | uutils (ms) | gnu / fern | uutils / fern |
|---|---|---|---|---|---|---|
| `csplit` | at line 4000000 | 73.84 ± 6.53 | 202.38 ± 14.60 | 307.41 ± 19.71 | 2.74× | 4.16× |
| `csplit` | at `/4000000/` | 91.30 ± 7.62 | 438.14 ± 28.42 | 329.38 ± 22.80 | 4.80× | 3.61× |
| `csplit` | at `/^4000000$/` | 82.12 ± 4.35 | 432.46 ± 27.22 | 353.35 ± 20.94 | 5.27× | 4.30× |
| `csplit` | at a never-matching regexp | 95.23 ± 5.98 | 586.37 ± 21.36 | 304.75 ± 18.08 | 6.16× | 3.20× |
| `csplit` | into 80 pieces | 107.52 ± 7.09 | 199.04 ± 19.22 | 325.51 ± 13.56 | 1.85× | 3.03× |
| `csplit` | at a literal-prefixed class | 81.24 ± 7.54 | 429.90 ± 31.43 | 329.92 ± 28.81 | 5.29× | 4.06× |
| `csplit` | at an alternation | 7189.63 ± 199.20 | 440.62 ± 23.76 | 327.17 ± 20.39 | 0.06× | 0.05× |

Reading it: csplit never materialises a line. The break point is found by
walking newlines with `__memchr` over whole read blocks, the piece is one
write of a byte range, and `lib/bre.fern` is asked about a RANGE of the
buffer rather than a string cut out of it — which is what the first draft
did, at 8 000 000 allocations for the workload.

The last row is the one that loses, and it is the regexp engine rather than
csplit (#8820): a pattern that is entirely a literal, or that STARTS with
one, is answered by a byte scan, and everything else runs the Thompson
simulation over every byte at several heap operations per position. An
alternation has no literal prefix, so the only filter left is the fastmap
— the set of bytes a match can begin with — which an alternation of
ordinary words barely narrows. `nl -bp`, `expr` and `tac -r` reach the
same engine, so the same work pays for all four.

`od`, 2026-09-07, Linux x86-64, the same 62 MiB file (and a 1 MiB one for
the float row), GNU 9.4:

| utility | workload | fern (ms) | gnu (ms) | gnu / fern |
|---|---|---|---|---|
| `od` | default (`-t o2`) | 6023 | 3252 | 0.54× |
| `od` | `-t x1` | 5677 | 5588 | 0.98× |
| `od` | `-t x1 -w64` | 4626 | 5394 | 1.17× |
| `od` | `-t x8` | 4888 | 944 | 0.19× |
| `od` | `-c` | 6405 | 5700 | 0.89× |
| `od` | `-A n -t x1` | 4676 | 5635 | 1.21× |
| `od` | `-S 4` | 568 | 515 | 0.91× |
| `od` | `-t f8` of 1 MiB | 6685 | 160 | 0.02× |

Reading it: od's cost splits into a per-FIELD part, which is close to
GNU's, and a per-BLOCK part, which is not. The three `-t x1` rows are the
same fields over 3.9 M, 977 K and 242 K blocks, and the ratio walks from
0.98 to 1.17 as the block count falls; `-t x8` has two fields a block and
is nearly all of the per-block cost. That cost is `u8[]`'s append, which
allocates rather than growing, and a byte buffer handed to a function,
which the callee's first write copies (#8498) — folding five call
boundaries into one already took `-t x1` from 11.0 s to 5.7. The float row
is a different gap: the shortest-round-trip search runs `lib/ld.fern`'s
EXACT decimal expansion once per attempt, where glibc has a purpose-built
dtoa. Both are #8828.

Group B's fifth, 2026-09-07, Linux x86-64, the same 62 MiB / 8 000 000-line
file, GNU 9.4 and uutils 0.0.24. The same 4-core container with other agents'
builds on it — the σ is mostly theirs, and the rows within 20% of parity are
not separable from the noise:

| utility | workload | fern (ms) | gnu (ms) | uutils (ms) | gnu / fern | uutils / fern |
|---|---|---|---|---|---|---|
| `split` | `-l 100000` of a 62 MiB file | 46.40 ± 20.48 | 90.30 ± 27.16 | 109.02 ± 23.28 | 1.95× | 2.35× |
| `split` | `-b 8M` of a 62 MiB file | 32.06 ± 19.54 | 18.85 ± 5.88 | 63.40 ± 31.07 | 0.59× | 1.98× |
| `split` | `-C 8M` of a 62 MiB file | 50.03 ± 37.38 | 43.71 ± 33.89 | 91.33 ± 25.84 | 0.87× | 1.83× |
| `split` | `-n 8` of a 62 MiB file | 41.79 ± 26.29 | 40.42 ± 29.02 | 88.17 ± 62.82 | 0.97× | 2.11× |
| `split` | `-n l/8` of a 62 MiB file | 34.64 ± 21.56 | 41.02 ± 27.84 | 319.98 ± 41.05 | 1.18× | 9.24× |
| `split` | `-n r/8` of a 62 MiB file | 1058.74 ± 178.33 | 317.38 ± 72.02 | 418.23 ± 83.65 | 0.30× | 0.40× |
| `split` | `-l 100000` from a pipe | 66.00 ± 29.74 | 112.12 ± 32.75 | 149.03 ± 39.35 | 1.70× | 2.26× |
| `split` | `-n 8` from a pipe | 86.40 ± 44.42 | 95.24 ± 38.26 | 102.48 ± 39.30 | 1.10× | 1.19× |

Reading it: every row that decides a whole BLOCK at a time is at or ahead of
GNU — `-l` with one `__count_byte` per block, `-C` with one `__rmemchr` per
piece, and the two `-n` divisions that need no record boundaries before the
chunk end. `-n 8 from a pipe` carries the spool to `$TMPDIR` and still wins,
because the spool is a copy at memory bandwidth and GNU pays it too.

`-n r/8` is the one row that loses, and it loses to #8770: round robin is the
only mode with per-RECORD work, and a record has to be copied into its file's
share one append at a time. Two shapes were measured before this one. Dealing
records into an array of `BufWriter` is 0.04× — a writer read out of an array
is aliased by the array, so appending to it copies the whole buffer instead of
growing it in place, which is quadratic per block. Batching a block's share
per file through the shared string buffer (`strbuf_append`) is what is here,
and it is 10x better and still 3x off GNU: what remains is 8 million
`slice_unchecked(...) + ""` materialisations, one per record, which is exactly
the append #8770 wants to fuse.

`tac`, 2026-09-06, the same container and the same 62 MiB file, with
uutils 0.0.24 (the Debian multi-call binary the script now finds) and a
588 KiB file — 100 000 lines of `seq` — for the regular-expression row.
Another agent was building on the same four cores for part of the run, so
read the ratios rather than the absolute numbers; GNU's own figure for the
plain copy moved 85 → 100 ms between two runs an hour apart.

These rows were taken BEFORE #8784's range-append fusion reached this
branch, so they understate the shipped build by roughly 8 ns of a 22 ns
record on the x86-64 and wasm throughput rows — see the emit bullet below.
The startup and `-r` rows are unaffected, and so is arm64:

| utility | workload | fern (ms) | gnu (ms) | uutils (ms) | gnu / fern | uutils / fern |
|---|---|---|---|---|---|---|
| `tac` | a 62 MiB file | 380.18 ± 24.13 | 100.27 ± 3.85 | 85.11 ± 4.41 | **0.26×** | **0.22×** |
| `tac` | `-b` of a 62 MiB file | 388.17 ± 11.12 | 95.56 ± 4.16 | 84.14 ± 4.24 | **0.25×** | **0.22×** |
| `tac` | `-s 5` of a 62 MiB file | 296.30 ± 9.47 | 87.29 ± 4.98 | 67.00 ± 2.99 | **0.29×** | **0.23×** |
| `tac` | a 62 MiB file from a pipe | 633.36 ± 38.48 | 185.29 ± 11.46 | 146.55 ± 13.84 | **0.29×** | **0.23×** |
| `tac` | `-r -s "[0-9]"` of a 588 KiB file | 193.42 ± 11.41 | 33.49 ± 2.52 | 13.11 ± 1.08 | **0.17×** | **0.07×** |
| `tac` | a one-line file | 0.28 ± 0.24 | 1.12 ± 0.34 | 2.11 ± 0.39 | 3.97× | 7.51× |

**tac does not meet the epic's bar.** It wins startup and loses every
throughput row by three to four times, and the cause is per-record rather
than algorithmic: both sides read the same 8 KiB blocks backwards and copy
each record once, and Fern's copy costs more. Measured on 8 000 000
records, one at a time:

- **the emit, ~14 ns a record.** `buf = buf + slice_unchecked(w, lo, hi)`
  into a buffer that resets at 8 KiB. It used to be ~22 ns — 12 for the
  append and 10 more for materialising the slice as its own string first —
  until #8784 fused that shape into `__fern_str_append_range`, which copies
  the range straight out of the source. Re-measured over 8 000 000 records
  against the same loop appending a literal, the slice now costs 1.6 ns on
  top of the 12.3 ns append. What is left is #8770's per-append floor,
  which is not something tac can arrange around. arm64 has no in-place
  append helper, so it keeps the old cost.
- **the scan, ~21 ns a record.** `__rmemchr` itself is 4.5 ns; the rest is
  the call returning `(i32, i32)`, which costs 8 ns against 1.3 for a
  scalar return.
- **the read, negligible.** 62 MiB backwards in 8 KiB blocks is 25 ms.

GNU's whole per-record cost — memrchr, memcpy, and the share of the
write(2) — is about 12 ns, so each of Fern's two halves alone is more than
GNU spends in total.

The first draft was 12× rather than 4×, and that part WAS arrangeable:
the 8 KiB buffer lived in a struct field and was appended to through a
helper, and neither shape keeps a string's in-place growth, so every
record copied the whole buffer (#8785 — `io_buffered.fern`'s BufWriter
documents that shape as the fast one and is wrong about it). The buffer is
a local in `tac_backward` appended to inline, which is the only shape that
grows in place today: 1.13 s → 0.39 s.

The `-r` row is a third engine again: a backward `re_search` runs the
thread simulation once per candidate start, with nothing but the
start-position filter — the literal prefix where the pattern has one, the
fastmap otherwise — to skip positions.

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

Group C's first four, 2026-09-07, Linux x86-64 (GNU coreutils 9.4; uutils
0.0.24 as the Debian multi-call binary):

| utility | workload | fern (ms) | gnu (ms) | uutils (ms) | gnu / fern | uutils / fern |
|---|---|---|---|---|---|---|
| `uname` | uname | 0.18 ± 0.36 | 0.99 ± 0.51 | 1.93 ± 0.48 | 5.60× | 10.89× |
| `uname` | uname -a | 0.18 ± 0.36 | 1.03 ± 0.46 | 1.92 ± 0.77 | 5.58× | 10.44× |
| `arch` | arch | 0.17 ± 0.36 | 1.09 ± 0.86 | 1.95 ± 0.65 | 6.46× | 11.52× |
| `nproc` | nproc | 0.23 ± 0.38 | 1.22 ± 0.58 | 2.37 ± 0.78 | 5.27× | 10.23× |
| `nproc` | nproc --all | 0.39 ± 0.61 | 1.27 ± 0.68 | 2.27 ± 0.71 | 3.26× | 5.81× |
| `nproc` | nproc --ignore=1 | 0.35 ± 0.58 | 1.25 ± 0.73 | 2.48 ± 1.61 | 3.53× | 6.99× |
| `pwd` | pwd | 0.21 ± 0.51 | 1.44 ± 1.36 | 2.05 ± 0.93 | 7.02× | 9.97× |
| `pwd` | pwd -L | 0.29 ± 0.47 | 1.19 ± 0.56 | 2.18 ± 0.65 | 4.10× | 7.48× |

All four are startup-bound, so the margin is the same static-binary one
`true` and `echo` measure, and `strace -c` says where it comes from: each of
these runs **four syscalls** — execve, the one the utility is about, write,
exit_group — against GNU's 38, which is the dynamic loader before main.

Nothing else in the table is signal. Every Fern row is 0.17–0.39 ms and every
difference between two of them is inside its own σ, `uname` against `uname -a`
included: one uname(2) fills the whole record whichever fields are asked for,
and one write puts them out.

**At this scale the σ is the machine, not the program.** A table taken while
another agent's bench has the same four cores comes back with σ larger than
the mean and these eight ratios spread over a factor of five — enough to
invent an explanation for a row that is not there. That is what the "only
comparable within one run on one machine" above costs when it is ignored, and
a sub-millisecond utility is where it costs the most.

Group C's identity slice, 2026-09-07, Linux x86-64 (GNU coreutils 9.4;
uutils 0.0.24 as the Debian multi-call binary). All five are
startup-bound, so the whole table is one comparison made five ways:

| utility | workload | fern (ms) | gnu (ms) | uutils (ms) | gnu / fern | uutils / fern |
|---|---|---|---|---|---|---|
| `whoami` | whoami | 0.33 ± 0.44 | 1.25 ± 0.56 | 2.20 ± 0.66 | 3.80× | 6.67× |
| `id` | id -u | 0.38 ± 0.48 | 1.44 ± 0.60 | 2.21 ± 0.63 | 3.77× | 5.77× |
| `id` | id -un | 0.34 ± 0.49 | 1.47 ± 0.59 | 2.37 ± 0.66 | 4.33× | 6.96× |
| `id` | id | 0.52 ± 1.01 | 1.46 ± 1.06 | 2.18 ± 0.43 | 2.81× | 4.20× |
| `id` | id -G | 0.23 ± 0.13 | 1.42 ± 0.17 | 2.18 ± 0.26 | 6.12× | 9.42× |
| `id` | id root | 0.33 ± 0.17 | 1.42 ± 0.17 | 2.23 ± 0.30 | 4.32× | 6.76× |
| `groups` | groups | 0.26 ± 0.32 | 1.31 ± 0.82 | 2.15 ± 0.28 | 4.94× | 8.12× |
| `groups` | groups root | 0.29 ± 0.19 | 1.24 ± 0.26 | 2.15 ± 0.26 | 4.20× | 7.32× |
| `logname` | logname | 0.24 ± 0.10 | 1.13 ± 0.18 | 2.58 ± 2.21 | 4.80× | 10.95× |
| `printenv` | printenv | 0.51 ± 1.01 | 1.32 ± 1.06 | 2.06 ± 0.70 | 2.58× | 4.01× |
| `printenv` | printenv -0 | 0.49 ± 1.05 | 1.23 ± 0.77 | 2.47 ± 1.01 | 2.49× | 5.01× |
| `printenv` | printenv PATH | 0.28 ± 0.70 | 1.18 ± 0.84 | 2.12 ± 0.75 | 4.24× | 7.61× |
| `printenv` | printenv four names | 0.25 ± 0.41 | 1.37 ± 1.09 | 2.54 ± 1.68 | 5.50× | 10.21× |

Reading it: this is `hostid`'s margin again, and for the same reason —
a static binary against a dynamic one that then dlopens NSS modules to
answer a question two file reads answer. GNU's floor here is its loader,
not its work: `id -u` reads nothing at all and still costs 1.4 ms, while
`id` (two database scans, both names and the group list) costs Fern
0.52. uutils pays the loader AND its multi-call dispatch.

The one row worth watching is `id`, which is the only one that reads
both databases: it is the slowest Fern column here and would be the
first to feel a /etc/passwd of any size, since the lookups are linear
scans with no index. Every one of them stops at its first match, and the
group names are resolved in ONE pass over /etc/group rather than a pass
per gid — the shape that would otherwise be quadratic in the group
count.

`sort`, 2026-09-07, Linux x86-64 (GNU coreutils 9.4, uutils 0.0.24). 500 000
lines of eleven lowercase letters, and for `-n` the same many signed ten-digit
integers:

| utility | workload | fern (ms) | gnu (ms) | uutils (ms) | gnu / fern | uutils / fern |
|---|---|---|---|---|---|---|
| `sort` | 500k lines | 369.47 ± 13.85 | 114.92 ± 7.06 | 112.37 ± 10.43 | 0.31× | 0.30× |
| `sort` | `-n` 500k numbers | 1061.38 ± 45.26 | 143.24 ± 15.83 | 190.42 ± 7.72 | 0.13× | 0.18× |
| `sort` | `-k2,2n` 500k lines | 2649.59 ± 79.30 | 138.27 ± 5.51 | 196.55 ± 7.14 | 0.05× | 0.07× |
| `sort` | `-k1,1` 500k lines | 1013.66 ± 31.01 | 112.99 ± 3.90 | 140.78 ± 6.86 | 0.11× | 0.14× |
| `sort` | `-u` 500k lines | 381.77 ± 21.53 | 110.85 ± 4.74 | 127.34 ± 27.82 | 0.29× | 0.33× |
| `sort` | `-r` 500k lines | 371.89 ± 24.12 | 97.47 ± 5.33 | 89.35 ± 4.63 | 0.26× | 0.24× |
| `sort` | `-s` 500k lines | 371.55 ± 21.99 | 97.01 ± 6.07 | 101.17 ± 8.78 | 0.26× | 0.27× |
| `sort` | 500k lines from a pipe | 365.45 ± 17.17 | 162.16 ± 9.46 | 100.13 ± 12.54 | 0.44× | 0.27× |
| `sort` | a sorted 500k-line file | 200.78 ± 10.00 | 37.49 ± 1.82 | 31.97 ± 2.39 | 0.19× | 0.16× |
| `sort` | `-c` a sorted 500k-line file | 45.65 ± 2.62 | 8.13 ± 0.96 | 16.26 ± 3.01 | 0.18× | 0.36× |
| `sort` | `-m` two sorted files | 242.28 ± 11.61 | 31.43 ± 2.70 | 95.14 ± 7.61 | 0.13× | 0.39× |

**This is the first utility here that is slower than GNU everywhere, and the
reason is not in the utility (#8822).** The algorithm is GNU's — a stable merge
over packed line offsets — and the cost is the price of one Fern instruction on
the x86-64 emitter: locals live in memory, a boolean goes through push/pop, and
one `text[i]` is thirteen instructions because the small-string check and the
bounds check are redone per access. On top of that `ir.Inline` does nothing at
all above 20 000 whole-program ops, which a coreutil with `lib/gnu.fern` and
`lib/ld.fern` clears easily, so a one-line predicate is a real call: expanding
the digit test by hand inside `magcompare` was worth a third of `sort -n`.
Roughly half of GNU's lead on the first row is threads (`--parallel=1` puts GNU
at 810 ms there); the rest, and all of the `-n` gap, is per-instruction cost.

Two changes inside the utility paid before that floor was reached, both
measured: holding a line as one packed i64 rather than two parallel offset
arrays (2.85 s to 1.95 s on 2M lines — two scattered reads per comparison
became one sequential one), and dropping a redundant NUL scan from the numeric
path. What is left there is a per-line cache of the first key's span, which is
what GNU's `struct line` carries and what would close most of the `-k` rows;
#8822 has the shape.

`tee`, 2026-09-07, Linux x86-64, the same 62 MiB file, GNU 9.4 and uutils
0.0.24:

| utility | workload | fern (ms) | gnu (ms) | uutils (ms) | gnu / fern | uutils / fern |
|---|---|---|---|---|---|---|
| `tee` | to stdout alone | 6.78 ± 2.26 | 10.48 ± 4.32 | 11.80 ± 2.10 | 1.55× | 1.74× |
| `tee` | to one file | 58.15 ± 6.56 | 73.38 ± 12.87 | 69.51 ± 7.26 | 1.26× | 1.20× |
| `tee` | to four files | 226.49 ± 42.66 | 397.84 ± 37.02 | 379.17 ± 53.67 | 1.76× | 1.67× |
| `tee` | from a pipe to one file | 62.22 ± 5.93 | 91.84 ± 8.02 | 96.34 ± 19.21 | 1.48× | 1.55× |
| `tee` | down a pipe | 61.87 ± 5.51 | 97.24 ± 4.07 | 105.35 ± 9.67 | 1.57× | 1.70× |

`tee` does no per-byte work at all, so the whole margin is the syscall
count: the read block is 64 KiB where GNU and uutils both read 8 KiB
(measured under strace), which is eight times fewer `read(2)`s and eight
times fewer `write(2)`s per output. The sweep that chose
it is in `read_size()` in `tee.fern` — 62 MiB to a file costs 106 ms at
4 KiB and 58.6 at 64 KiB, and past 64 KiB the pipe case stops improving
because a write bigger than the pipe buffer puts writer and reader in
lockstep, the same effect `yes.fern` measured.

**Read a single bench run's ratios with the σ next to them.** An earlier
run of this same table, on a busier machine, put the four-file row at
298.50 ± 119.91 against uutils' 270.02 and would have recorded `tee` as
0.90× uutils there. The σ was 40% of the mean and the row was noise; a
second run with σ at 19% has it at 1.67×. Neither number is wrong about
the machine it ran on, which is why every table here says to compare
only within one run.

Group C's `link` and `unlink`, 2026-09-07, Linux x86-64 (GNU coreutils 9.4; uutils
0.0.24 as the Debian multi-call binary). Both are one syscall, so the whole
number is process startup — which is why the 200-operand rows, where that cost
is paid 200 times, separate the three implementations so much further than the
one-operand rows do:

| utility | workload | fern (ms) | gnu (ms) | uutils (ms) | gnu / fern | uutils / fern |
|---|---|---|---|---|---|---|
| `link` | link one hard link | 1.49 ± 0.29 | 2.28 ± 0.56 | 3.28 ± 0.68 | 1.53× | 2.19× |
| `link` | link 200 hard links | 34.34 ± 2.51 | 210.93 ± 8.47 | 386.25 ± 11.11 | 6.14× | 11.25× |
| `unlink` | unlink one file | 0.32 ± 0.23 | 1.33 ± 0.63 | 2.17 ± 0.30 | 4.12× | 6.73× |
| `unlink` | unlink 200 files | 37.05 ± 4.66 | 214.35 ± 8.40 | 416.24 ± 11.96 | 5.79× | 11.23× |

The one-operand rows carry a constant the others do not: the run has to undo
what the previous one did, so the `rm` that clears the link (and the shell
redirection that recreates the file) is inside the timed command. It comes from
the GNU directory for all three implementations, exactly as `yes`'s `head`
does, so it compresses every ratio equally rather than biasing one.

## The primitives group C is built on

A utility here is blocked on a builtin far more often than on anything about
itself, and a builtin is four classifications plus a self-host mirror
(`docs/FREESTANDING-CORE.md`, `docs/PACKAGE-CAPABILITIES-BRIEF.md`). The
directory and link family landed as one set (#8883), because they share an
emitter shape on every backend and splitting them would have paid the
registry cost five times:

    create_dir(path, mode)        mkdirat, with EEXIST reaching the caller
    remove_dir(path)              unlinkat + AT_REMOVEDIR, i.e. rmdir(2)
    create_link(target, path)     linkat, no AT_SYMLINK_FOLLOW
    create_symlink(target, path)  symlinkat; the target is stored verbatim
    read_link(path)               readlinkat into a PATH_MAX buffer
    umask(mask)                   umask(2), which sets and reads in one step

The five filesystem ones take `fs` and WASI implements every one of them, so
they are provided on wasm rather than classified out. `umask` takes `fsmode`
beside `access` and `write_file_exec` and is refused there — WASI has no
file-mode creation mask, and `path_create_directory` has no mode either, which
is the same fact twice. `docs/FREESTANDING-CORE.md` carries both.

What is deliberately NOT here: `create_dir_all` and `remove_dir_all`, which
already existed. Neither is the primitive `mkdir(1)` or `rmdir(1)` needs —
the first folds every EEXIST into `Ok(())` and cannot say whether it created
anything, the second drains a tree and ignores a missing target — and the
errno they discard is the whole of what those two utilities report.

## Known divergences

**`od -t fL` prints a canonical value for an encoding x87 never
produces.** The 80-bit extended format has bit patterns that are not
values: an unnormal (a non-zero exponent with the stored integer bit
clear) and a pseudo-denormal (a zero exponent with it set). od.fern reads
the first as NaN, which is what the FPU answers and what GNU prints, and
the second as glibc's `__mpn_extract_long_double` does — the 63-bit
fraction, normalised, with the integer bit contributing nothing. That
agrees with GNU on every pseudo-denormal measured except one whose only
set bit IS the integer bit, where GNU prints the value the bit would have
carried, and it can differ in the last place for the others because
GNU's shortest-round-trip search cannot round-trip a value `strtold` will
not produce. Real data does not contain these: a pseudo-denormal needs a
zero exponent field under a set integer bit, which no computation writes.

**`csplit -w0` and `od -w0` are not in the corpus.** GNU 9.4 aborts on
`csplit -b` with a zero-width block — `bytes_per_block` comes out 0 and a
later assertion fires, with no diagnostic and a SIGABRT — and od's own
width handling past INT_MAX prints integer-overflow artefacts rather than
answers (`od -w2147483648` runs the fields together, `od -w4294967296`
dumps nothing). Reproducing a crash is not a behaviour worth matching,
and Fern has no `abort()` to match it with; od refuses a width past
INT_MAX with GNU's own `memory exhausted`, which is what GNU says for a
width of a terabyte.

**`od -S` between the host's memory and PTRDIFF_MAX.** GNU sizes a buffer
from the minimum string length and allocates it before it scans, so a
length larger than the machine can allocate is `memory exhausted` rather
than a run nothing reaches — on the dev container the break is somewhere
between 8 GiB, which it accepts, and 64 GiB, which it does not. That
boundary is the host's, so od.fern refuses only at PTRDIFF_MAX and above,
where no host can serve the allocation and glibc's malloc always fails.
Between the two it prints nothing and exits 0, as GNU does on a machine
with the memory. The corpus covers both deterministic sides and nothing in
the band.

**`tac` holds a non-seekable input in memory.** tac reads its input
backwards, so a pipe has to be stored before the first record can be
written. GNU spools it to an unlinked `$TMPDIR/cutmpXXXXXX` and keeps one
8 KiB block resident; `tac.fern` holds the whole stream instead, because
creating that temporary safely needs a file created EXCLUSIVELY at a
chosen path and `open_writer` truncates whatever the name reaches
(#8776; `temp_dir()` is exclusive but chooses the directory itself, so it
cannot take gnulib's `$TMPDIR`-only-if-it-is-a-directory rule). Nothing
in the bytes differs: the held stream is handed back in the SAME 8 KiB
windows the temporary file would be read in, so `-r` meets the same
buffer boundaries and `^` anchors in the same places. What differs is
memory — the input's size rather than a block — and that GNU's
`failed to create temporary file` is unreachable here, so an unwritable
`$TMPDIR` under an unprivileged user fails on GNU and succeeds on this.
**`uname -p` and `-i` print the machine name, as Linux distributions'
GNU does.** Upstream coreutils can answer neither on Linux — the two
`#if`s in uname.c are a Solaris `sysinfo(2)` and a BSD `sysctl`, and glibc
has neither — so an upstream build prints `unknown` for both and `-a`
omits them. Every distribution patches that to the machine name: Debian,
Ubuntu, Fedora and RHEL all ship it, `setarch linux32 uname -p` follows
`-m` to `i686`, and the binaries this corpus is compared against on the
Ubuntu runners are among them. So that is what `uname.fern` prints, and
the `-a` omission rule is live only on Darwin, where upstream's own
answers stand: `-p` is the CPU family (`arm`, not `arm64`) and `-i` is
genuinely unknown, so `-a` drops it. The one environment where this
diverges is a distribution shipping unpatched coreutils — Arch is the
example — where the corpus fails loudly on the `-p` / `-i` / `-a` cases
rather than passing something wrong.

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
The search loop itself is `__res_context_search`'s, and the shape that
matters is that **the as-is query is not one candidate among the others**
— reading it as one gets two things wrong, and both were got wrong once.
It sits outside the loop's stop rules, so however it fails the search
list is still tried; and its status is saved and reported in preference
to anything the suffixes produce, so an as-is NXDOMAIN reports NOTFOUND
even when a later suffix hit a SERVFAIL. When ndots is satisfied it LEADS
and there is no retry afterwards; otherwise it TRAILS and is the retry,
which runs whether the suffix loop finished or was cut short. Within the
suffixes, a SERVFAIL records itself and moves to the next, a refused
connection returns at once trying nothing further, NXDOMAIN moves on, and
anything else ends the loop.
Neither changes the bytes on a host whose name resolves.

**`split --filter=COMMAND` is refused.** GNU forks per piece, hands the child
the read end of a pipe as its stdin, and streams the piece into the write
end. Fern has `proc_fork` / `proc_exec` / `proc_waitpid` but no `pipe(2)`, no
`dup2(2)` and no way to set a variable in a child's environment, and
`subprocess()` is interp-only and takes the child's whole stdin as a string
built in advance — which a piece that may be gigabytes is not. So the option
is DECLARED, because its getopt behaviour is observable whether or not it
runs (a required argument, a place in the `--f` prefix space, a position in
the ambiguity list), and using it prints `split: --filter is not supported on
this system` and exits 1 where GNU would run the command. The primitive is
#8810; unlike `tail --pid`, GNU has no degraded path of its own here to
borrow, so this one is a real divergence rather than a shared one.

**`split --hex-suffixes=FROM` where FROM holds a hex LETTER is not
reproduced.** GNU 9.4 seeds its suffix counter with `FROM[i] - '0'`, which is
right for the decimal digits and 39 too large for `a`–`f`, and then indexes
its 16-character alphabet with the result. The names that come back are the
bytes that follow that string literal in the binary: `--hex-suffixes=a` gives
`x0a`, `x0e`, `x10`, `x11`, …, and `--hex-suffixes=c` gives `x0c`, `x00`,
`x01`, … — non-monotonic, repeating, and a property of one build's `.rodata`
rather than of split. Fern counts in hex from FROM. FROM written in decimal
digits (`--hex-suffixes=10`) agrees byte for byte and is in the corpus; a
FROM with a letter is not, because there is nothing to agree with.
**What `tac`'s write-failure cases assume about the host.** Whether a
failed stdout write is reported as `write error: No space left on device`
or as a bare `write error` is decided by which bytes glibc's stdio still
had pending at fclose, so the four corpus cases that pin the boundary
(4000 and 4096 bytes, 12000 and 13192) are reading a specific buffer size:
BUFSIZ 8192, or `st_blksize` when fstat reports something smaller, which
every pipe, device and ordinary file on Linux does at 4096. `lib/gnu.fern`'s
`Stdio` reproduces that choice from the descriptor rather than assuming
4096, so the cases hold wherever GNU's own do — but a host whose stdout
reported a different `st_blksize` would move the boundary for BOTH
binaries, and these four sizes would stop being the interesting ones. They
pin an algorithm, not a constant.

**`id` on an SELinux-enabled kernel is untested.** `id -Z` and the
` context=` suffix of the composite line are implemented — libselinux's
own two steps, the selinuxfs mount in /proc/self/mounts and the context
in /proc/self/attr/current — but no machine the corpus runs on has
SELinux, so the only path the oracle has ever compared is the refusal
(`--context (-Z) works only on an SELinux-enabled kernel`) that a kernel
without it gives. The detection is what keeps that refusal honest rather
than unconditional; what an SELinux host prints is reproduced from the
documented behaviour and not from a reference binary.

## Open gaps

**`X as usize` means different addresses in the two compilers (#8799).**
Native reads the cast as a counted buffer's DATA pointer, which is what
`std/string`'s `bytes()` is written against; the self-host reads it as the
BOX, whose first word is the length. Code that reads or writes through it is
therefore correct under one compiler and off by a header under the other,
with no diagnostic either way. Invisible until something compiles such code
BOTH ways, which nothing did before the self-host leg: `.bytes()` is an
intrinsic there, so the one stdlib site never reaches the self-host's
lowering. `base64` wanted it — raw scratch buffers run the encode at 165 ms
against the 460 ms `u8[]` with `.with()` costs — and ships without it.
**A process cannot read its own resource limits (#8819).** GNU `sort` caps
`--batch-size` at what `getrlimit (RLIMIT_NOFILE, …)` reports minus the three
standard descriptors, and names that number when a value exceeds it:
`maximum --batch-size argument with current rlimit is 19997`. Fern has no way
to ask, so `sort.fern` reproduces the option's other two diagnostics exactly
and accepts any value at or above the minimum of 2. `--batch-size` changes no
byte of output on either side — it is an external-merge fan-in, and this sort
holds the whole input in memory — so the gap is one diagnostic pair, and the
corpus carries no case above the cap until the primitive exists.


**A process-liveness query (#8767).** `tail --pid=PID` stops following once
that process exits, which GNU asks as `kill (pid, 0)`. Fern can run a child
and wait for it, but cannot ask whether an ARBITRARY pid is still alive, so
`tail.fern` validates the operand exactly as GNU does and then takes GNU's
own not-supported-on-this-system path: `tail: warning: --pid=PID is not
supported on this system`, and it follows without it. That is a real
degraded path in GNU rather than a divergence invented here, so the corpus
compares equal — but the option is not implemented until the primitive is.

**A string append costs 8-16 ns whatever its size (#8770).** Every
line-oriented utility assembles its output by appending to a local, and the
floor is per append rather than per byte, so `cat -n` is 0.22× GNU and
`cat -A` 0.18× while plain `cat` is at parity. The one shape that HAS been
fused is `acc = acc + slice_unchecked(s, a, b)`: #8784 lowers it to
`__fern_str_append_range`, so the slice is no longer materialised as its own
string first — worth 8 ns of a 22 ns record in tac's emit loop, on x86-64
and wasm only. The per-append floor underneath it is what remains open.
Two rewrites of `cat -n` measured as a wash and were reverted rather than
kept, which is what located the cost. Neighbours: #8530 (`array.with`, struct updates) and
#8532 (small value structs boxed).

**A line RECORD costs ~365 ns to build and thread (#8815).** `join` is the
first utility here that holds TWO input cursors at once and carries a parsed
line — text, field bounds, join-field range — from one loop iteration to the
next, and it is 0.10x GNU on every workload. Of 1.05 s on the input side of a
2M-line run, ~0.32 s is reading and splitting and ~0.73 s is the record and
the cursor crossing three call boundaries per line, because a threaded cursor
has to come back through a tuple where C would mutate in place. Three rounds
of shaving (the join field held as a range rather than a sliced string, the
matched group off its arrays in the one-line case, the cursor rebuilt once
instead of three times) were worth 3-8% each and located the floor rather
than removing it: `array.append` at ~19 ns and a tuple return at ~20 ns
against a struct return's ~3 ns. It is a different shape from #8425
(per-byte) and #8770 (per-append), and every remaining group-B utility with
two cursors will meet it.

**Signal disposition control (#8792).** `tee` is blocked on it and is not
written yet. `-i` is `signal (SIGINT, SIG_IGN)`, and the whole `-p` /
`--output-error` family turns on whether SIGPIPE is ignored: GNU leaves it
at its default so `tee` DIES of SIGPIPE, and the moment either option is
given it ignores it so the write returns EPIPE and the mode picks one of
four behaviours. Four of those five rows are unreachable without the
primitive, and a `tee` that accepted the options and did nothing would be
exactly the carve-out this document forbids.

**A byte-range comparison costs a copy (#8791).** This one is performance,
not parity. `slice_unchecked` lowers to `__str_slice`, which COPIES —
`docs/STR-VIEW-CONTRACT.md` §1 records that native is safe from the
dangling-view class precisely by not implementing the view — and an indexed
byte loop runs at ~2.8 ns a byte against memcmp's ~0.35. There is no third
option: the builtin surface carries every SIMD kernel except the comparison
one. It is the whole of `uniq`'s remaining distance from GNU and most of
`comm`'s (with the order check off, `comm` is within noise of it), and it
will be `sort`'s and `join`'s too.

Gaps that are closed, each now exercised by the corpus rather than carved
out of it: `IoError.Other` carrying no strerror text (#8265), in the
write-failure cases (`yes >&-`, `> /dev/full`); source unable to learn its
compile target (#8338), in `yes.fern`'s per-target block; and fstat/lseek on
a DESCRIPTOR (#8713), which `cat` needs to refuse a closed fd 1 before it
reads anything and `tail` needs to read a regular file from its end — landed
as `r.stat()` / `w.stat()` / `r.seek()` on every backend. `hostid` wanted a
primitive rather than a fix — `hostname()`, gethostname(2) on every backend
(#8529) — and got it under its own capability rather than a one-off syscall
on one backend. Group C's first four wanted three more the same way:
`uname_field(i)` and `cpu_count()` under `sysinfo`, `getcwd()` under `cwd`,
each refused on both wasm worlds by E066 rather than answered with a
fiction — neither WASI preview has a utsname record, a processor count or a
current directory. A gap met later gets an issue and a fix, never a corpus
carve-out.

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
  `b2sum` `tee`. Done: `cat`, `tac`, `head`, `tail`, `wc`, `nl`, `cut`,
  `paste`, `join`, `comm`, `uniq`, `sort`, `tr`, `fold`, `expand`, `unexpand`,
  `split`, `csplit`, `od`, `base32`, `base64`, `basenc`, `tee` and the seven
  checksum utilities. `tee` wanted signal dispositions (#8792) for `-i`
  and its `--output-error` family: SIG_IGN on SIGINT and SIGPIPE.
  Needs a buffered stdout writer in `std/io_buffered`
  (its own header already promises one) and a streaming stdin reader whose
  reads can FAIL: every one of these reaches a read error through a directory
  operand, and `Reader.read_chunk` answered None to EOF and to EISDIR alike
  until #8700 gave it `Result[string, IoError]`. The hash
  utilities have their digests: `std/crypto` streams MD5, SHA-1,
  SHA-224/256/384/512 and BLAKE2b (`h = h.update(chunk)` per `read_chunk`
  piece), and `std/hash` has cksum's CRC-32 and both sum(1) checksums with
  their block counts. `tail -f` waits for group C.
- **C. needs a runtime primitive first** — everything that reads the process
  or the filesystem beyond `read_file` / `stat` / `read_dir`: `pwd`
  (getcwd), `tty` (ttyname), `nproc` (affinity), `uname` `arch` (uname)
  — those four done, on `getcwd()`, `cpu_count()` and `uname_field(i)`
  under the new `cwd` and `sysinfo` target capabilities —
  `whoami` `id` `groups` `logname` (done) `users` `who`
  `pinky` (uid, passwd, utmp), `printenv` (done) `env` (the whole
  environ, exec), `ln` `readlink`
  `realpath` (link, symlink, readlink; `link` and `unlink` are done),
  `mkdir` `rmdir` `rm` `mv` `cp`
  `install` `touch` `truncate` `mkfifo` `mknod` `mktemp` `sync` (rename,
  utimensat, ftruncate, mknod, fsync; `mkdir` with a mode and `rmdir` are
  primitives now), `chmod` `chown`
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
