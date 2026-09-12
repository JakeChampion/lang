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

A case may also name a `timeout`, which exists because the REFERENCE does not
always terminate. GNU `expr bba : '\(b\|\|a\)\?*'` never returns — two of
them were found at 95% CPU eighteen minutes after the sweep that started them
was killed — so an unbounded differential run hangs instead of naming the input
that hung it. The bound is per-invocation and zero everywhere else, so no
existing case changes behaviour.

A case may also name a `umask`, for a utility whose answer is the creation
mask applied to something. `mkdir` is what needs it: the whole subject is which
mode a directory ends up with, and `-m` changes which CLAUSES of a MODE the mask
still reaches rather than switching it off. A mask is process-global state a
child inherits at fork and cannot be set per-child, so a case naming one runs
with every other case excluded and the mask is restored before the next starts;
every other case holds the read side of the same lock and they still run
concurrently, which the self-host leg needs.

A case may also name a `mask`, for the one utility whose correct answer
differs run to run: `mktemp`'s whole output is a run of random characters.
The mask rewrites that run BY POSITION on stdout and in the names of the
entries the run left behind — never on stderr, which carries the template
with its X's intact — so the directory, the prefix, the suffix, the length,
the status and the tree all stay under byte comparison and only the
characters themselves are canonicalised. A seeded name is left alone: it is
identical on both sides already, and masking `td1` and `td2` under a
three-character mask would put two entries under one name. The hard-link
groups are renumbered afterwards, because the walk order a random name sorts
into is not the same on both sides. What a mask hides is what the corpus
stops proving, so `mktemp`'s test file states for each case which of the two
it is under, and the two properties no diff against GNU can see — that the
characters come from `[0-9A-Za-z]` and actually vary, and that the retry is
bounded at 62^3 — are Fern-side invariants beside the corpus rather than
cases in it.

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

### The three exemptions

Three outputs are ours by design, because their content names the
implementation:

- `--version` prints `<util> (Fern coreutils) <version>` and nothing else.
  Claiming GNU's version string would make the one output that identifies
  the program lie about which program it is.
- `--help` is our own text. GNU's is GPL-licensed prose carrying GNU's URLs,
  authors and (in 9.x) terminal hyperlink escapes; reproducing it would be
  copying, and it would be wrong in every particular that matters.
- `cksum --debug` — "indicate which implementation used" — is silent. GNU's
  CRC has several implementations and it picks one at startup by asking the
  CPU (`using pclmul hardware support` where the instruction exists), which
  is a runtime dispatch a static Fern binary with no CPU detection does not
  have; claiming the message would say something untrue about our own code,
  and printing a different one would diverge just the same. Only the CRC
  reaches it — GNU says nothing under `--debug` for the other ten
  algorithms, and neither do we, so those ARE in the byte-exact corpus, as
  is everything else about the option: that it is accepted, that it refuses
  a value, and that it stands in the ambiguity list.

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
comment and is the exception, not the pattern. There is exactly one such case:
`numfmt`'s buffer-length refusal is GNU <= 9.4 behaviour, pinned by a comment in
both `coreutils/numfmt.fern` and its corpus, and #8765 holds the open question
of whether Fern should follow 9.5+ instead. Nothing forces that today — every
CI runner and this container ship 9.4 — so it is a future-GNU decision rather
than a live divergence.

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
corpus registered, so a new one cannot join the tree without joining this
leg. Each `<util>_test.go` registers its own cases from an `init`, rather
than every utility appending to one map: that map was the file every open
coreutils PR conflicted on (#8840).

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
                    POSIX basic for expr, syntax 0 (Emacs) for tac -r
                    and for ptx's -W and -S, anchored or searched over
                    a range of a buffer for nl and csplit, forwards for
                    ptx's context scan and backwards for tac's, with a
                    literal, a literal-prefix and a
                    per-alternation-branch fast path ahead of glibc's
                    fastmap and the simulation, and glibc's regerror
                    texts as the diagnostics
  lib/ld.fern       C's `long double` as the TARGET has it, for the
                    utilities that convert and compute in one
                    (printf, numfmt, seq, sleep)
  lib/base.fern     the encoder / decoder base64, base32 and basenc
                    share: one codec parameterised by alphabet, block
                    and padding, plus every decode rule and diagnostic
  lib/tabs.fern     the `-t` tab-stop grammar and lookup expand and
                    unexpand share
  lib/digest.fern   md5sum, sha1sum, sha224sum, sha256sum, sha384sum,
                    sha512sum, b2sum and the eight digests of cksum,
                    which GNU also builds from one source: the option
                    surface, the file-name escaping and the check-line
                    grammar, parameterised by the digest each utility
                    names. cksum widens two rules of that grammar and
                    the module carries both behind one flag — a base64
                    digest is read wherever a hex one is, and a line's
                    TAG chooses the algorithm when no -a did. The three
                    checksums cksum offers that are NOT digests (the
                    POSIX crc, and sum's bsd and sysv) are std/hash
  lib/pwdb.fern     /etc/passwd and /etc/group as glibc's `files`
                    backend reads them — the lookups by name and by id,
                    getgrouplist's ordering, the process's own group
                    set, and the gecos field finger reads as a real name
                    (`&` is the login name capitalised) — for whoami,
                    id, groups, logname and pinky
  lib/utmp.fern     the login-accounting record: the fixed-size utmp
                    entry, the scans over it, and the terminal a ut_line
                    names — its mode is the message status and its atime
                    the idle time — for logname, users, who and pinky.
                    The line itself is lib/tty.fern's ttyname minus the
                    /dev/ prefix the field does not carry. The record's
                    TAIL is per-TARGET: glibc keeps ut_session and ut_tv
                    at 32-bit widths where __WORDSIZE_TIME64_COMPAT32 is
                    1, which is 384 bytes on x86-64, and uses a `long`
                    and a real struct timeval where it is 0, which is
                    400 on arm64
  lib/tz.fern       the local zone as tzset(3) finds it — the TZif file
                    $TZ names (absolute, or under $TZDIR), the POSIX
                    rule in its footer past the transition table, and
                    the rule string itself when no file answers — with
                    the offset AND the abbreviation (`EST`, `+0545`) in
                    force at an instant
  lib/timefmt.fern  C-locale nstrftime over the broken-down LOCAL time
                    lib/tz.fern resolves: gnulib's `-` `_` `0` `^` `#`
                    flags, an optional field width, the `E` / `O`
                    modifiers the C locale has no alternative for, and
                    the `:` repetitions of `%z`, with an unknown
                    conversion copied out percent and all. `%z` / `%Z`
                    read off the same lookup the fields came from, so a
                    stamp and its zone can never name different
                    instants. For du, pr, stat, ls, who, pinky and date
  lib/sys.fern      the five fields of the kernel's utsname record, by
                    name, for the utilities that print the record
                    (uname) or one field of it (arch)
  lib/resolv.fern   glibc's IPv4 name lookup — /etc/hosts, the
                    `hosts:` line of nsswitch.conf, resolv.conf and an
                    RFC 1035 A query — and getaddrinfo's AI_CANONNAME
                    over the same walk, for the utilities that resolve
                    the machine's own name (hostid) or one a session
                    recorded (who --lookup)
  lib/canon.fern    the symbolic-link resolution walk readlink -f/-e/-m
                    and realpath share: one loop over a name's
                    components under three existence modes, plus the
                    no-symlinks variant realpath -s wants. The two
                    rules that are in no man page live here — a suffix
                    of `/`, `/.` or `/..` makes the component before it
                    have to be a searchable directory whatever the mode
                    says, since a `..` pops lexically and nothing else
                    ever asks about what it popped; and the loop
                    detection is a (parent directory, remaining path)
                    set consulted from the twenty-FIRST link rather
                    than a depth limit, so a 5000-link chain resolves
                    and a cycle's residue depends on its LENGTH
  lib/mode.fern     the MODE operand `chmod` and every option spelled
                    "as in chmod" share — `mkdir -m` is the first
                    caller. The clause grammar, with the three rules no
                    man page states (a numeric perm takes no who and
                    ENDS its clause, so `=7,u+r` is two clauses and
                    `=7=7` is nothing; the `[ugo]` copy form must end an
                    action, so `u=g+w` parses and `u=gr` does not; and
                    `s` reaches S_ISUID through `u` and S_ISGID through
                    `g` while `t` reaches S_ISVTX through `o`, so `u+t`
                    and `o+s` change nothing); the arithmetic against a
                    creation mask, which reaches a clause that named no
                    who and nothing else, and not even that one when the
                    perm is numeric; and `=` on a DIRECTORY keeping the
                    set-user-ID and set-group-ID bits the entry already
                    had. The two outputs beside the mode are what
                    `mkdir` decides its post-creation chmod from: the
                    bits the MODE MENTIONED, and whether the change went
                    near the set-id pair with `+` or `-`
  lib/blocks.fern   the SIZE argument du and df share, and the number
                    it scales: the two alphabets — which are not the
                    set of letters that scale — and the three
                    diagnostics a bad spec earns, gnulib's
                    human_readable with the ceiling rounding both
                    print under, the unit a bare `-BK` puts after
                    every number (read off the DIVISOR, so an inode
                    column under `-BKiB` prints a bare `B` beside a
                    block column printing `KiB`), and the block-size
                    environment variables, where the first one SET
                    wins whether or not it parses
  lib/lines.fern    the two line-boundary scans head and tail share when
                    they hold bytes back — head -n -N because the last
                    N lines are the ones to elide, tail -n N because
                    they are the ones to keep. Either utility asks the
                    same question of each chunk, from opposite ends.
                    tail_start answers -1, never 0, when the chunk does
                    not hold N+1 terminators: 0 is indistinguishable
                    from "they begin at byte 0", and acting on the
                    second reading releases a hold that was part of the
                    withheld lines (#9064). read_size is deliberately
                    NOT shared — head reads 64 KiB and tail 8 KiB,
                    matching the reference binaries, and a chunk size
                    is not a line-boundary rule
  lib/tty.fern      ttyname(3) as glibc answers it: the /proc/self/fd/N
                    readlink first, trusted only when it still stats to
                    the same character device, then a walk of /dev/pts
                    and /dev by device number, for the utilities that
                    name the terminal on standard input (tty, logname,
                    who -m).
                    /dev/ptmx and /dev/pts/ptmx share a device number,
                    which is why the order matters
  lib/selinux.fern  is_selinux_enabled() and getcon(), which are two
                    files rather than libselinux — the selinuxfs line
                    of /proc/self/mounts and /proc/self/attr/current —
                    for id and runcon
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
scripts/coreutils-bench.d/
  <util>.sh         that utility's workloads, sourced by the bench with
                    the utility name in $1. A `_`-prefixed file is a body
                    several utilities share (the digests, the base
                    encodings); each member has its own file sourcing it
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
   paths once #8265 lands. Register them with `registerCorpus` from that
   file's `init`, so the self-host leg runs the same corpus. Run the gate;
   iterate until it is green.
5. Add the utility's workloads as `scripts/coreutils-bench.d/<util>.sh` and
   record its first numbers in the sub-issue. If it is slower than GNU, that is the
   next task, not a footnote.
6. Any Fern quirk or bug you hit on the way gets an issue and a fix, never a
   workaround. That is the project's standing order and it is doubly so here,
   where the whole exercise is to find them.

## Performance

`shuf`, 2026-09-11, Linux x86-64 (GNU coreutils 9.4; uutils 0.0.24 as the
Debian multi-call binary), a 62 MiB / 8 000 000-line file. Best of three
wall-clock runs rather than mean plus sigma: the hyperfine run took longer
than the box could give it with other agents on the same four cores, and its
sigma came back larger than its means. Comparable down its own columns only.

| utility | workload | fern (ms) | gnu (ms) | uutils (ms) | gnu / fern | uutils / fern |
|---|---|---|---|---|---|---|
| `shuf` | `-i 1-1000000` | 782 | 181 | 193 | 0.23x | 0.25x |
| `shuf` | a 62 MiB file | 7397 | 4244 | 1130 | 0.57x | 0.15x |
| `shuf` | a 62 MiB file from a pipe | 4696 | 3780 | 964 | 0.80x | 0.21x |
| `shuf` | `-n 10` of a 62 MiB file | 1711 | 360 | 200 | 0.21x | 0.12x |
| `shuf` | `-n 1000000` of a 62 MiB file | 2327 | 1705 | 310 | 0.73x | 0.13x |
| `shuf` | `-r -n 1000000` of a 62 MiB file | 1263 | 563 | 576 | 0.45x | 0.46x |
| `shuf` | `-n 10 -i 1-1000000000` | 0.58 | 1.60 | — | 2.76x | — |
| `shuf` | `-e` 200 operands | 1.07 | 1.67 | 2.82 | 1.56x | 2.65x |

**shuf does not meet this epic's bar.** It wins the two startup rows and
loses every per-line one, and the cause is per-draw and per-line rather than
algorithmic: 8 000 000 draws plus their one-byte writes cost Fern 1506 ms
against GNU's 720, so the generator alone is 2x, and the rest of the `-n 10`
row is `io_buffered.LineReader` materialising a string per line on the
reservoir path. That is #8770's per-append floor and #8822's x86-64 emitter.
Two shapes were fixed before these numbers were taken: the generator state
used to be rebuilt as a nine-field record per BYTE, and the output block used
to be a concatenation per line.

The row uutils cannot answer at all is `-n 10 -i 1-1000000000`: it
materialises the range and dies allocating 24 GB. GNU keeps a sparse map of
the slots a swap moved, and so does `shuf.fern`, which is the 0.58 ms.


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
one, is answered by a byte scan, and everything else ran the Thompson
simulation over every byte. That row is the state before the branch
literals landed — `lib/bre.fern` now takes the literal each ALTERNATION
branch opens with, of which every match begins with one, so a scan per
branch rejects a line that carries none and the earliest hit is where the
engine starts. What is left without a filter is a pattern whose branches
open with a class rather than a literal (`/[0-9]zzz/`), which is the lazy
DFA's job and not done.

Measured again after that on the same 62 MiB file, 2026-09-11, on a
DIFFERENT 4-core x86-64 host with no hyperfine on it — so these are the
best of three wall-clock runs rather than mean ± σ, and nothing here is
comparable to the table above, only down its own columns:

| workload | before (ms) | after (ms) | gnu (ms) |
|---|---|---|---|
| a literal, `/4000000/` | 109 | 112 | 520 |
| a literal prefix and a class | 92 | 99 | 508 |
| a two-branch alternation | 2116 | 169 | 517 |
| a four-branch alternation | 3368 | 351 | 513 |
| a 7-byte literal nowhere in the file | 5508 | 199 | 666 |
| a 3-byte literal prefix nowhere in it | 6233 | 250 | 693 |
| a class before a literal | 12378 | 11298 | 1549 |

Read the two alternation before-numbers with the engine's own bug in
mind: the scan they were measured on stopped at the first position with
no live thread (#9050), which ended most lines early and answered some of
them wrongly. The corrected scan without the branch literals is 3968 and
5709 ms.

The two rows that are nowhere in the file are a second bug the first one
hid: `__memchr` takes a start and no end, so a literal absent from the
LINE sent the vector pass on through every line after it, which made the
filter quadratic in the read block.

`nl -bp`, `expr` and `tac -r` reach the same engine, so all four utilities
are paid for at once.

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

Group C's `pathchk`, 2026-09-11, Linux x86-64 (GNU coreutils 9.4; uutils 0.0.24 as
the Debian multi-call binary). The default mode is one `lstat` per name, so the
one-operand rows are startup and the 200-operand ones are the per-name cost above
it; `-p` makes no syscall at all, and the last row is the only shape that reaches
the per-directory `statfs` walk. The box had four contended cores and sigma came
back larger than several of the means, so these are indicative and comparable only
down their own columns:

| utility | workload | fern (ms) | gnu (ms) | uutils (ms) | gnu / fern | uutils / fern |
|---|---|---|---|---|---|---|
| `pathchk` | one existing path | 1.40 ± 3.70 | 4.48 ± 12.61 | 8.27 ± 12.30 | 3.20× | 5.91× |
| `pathchk` | 200 existing paths | 1.59 ± 5.57 | 4.31 ± 11.84 | 9.32 ± 11.31 | 2.72× | 5.88× |
| `pathchk` | 200 missing paths | 3.58 ± 10.83 | 4.69 ± 9.05 | 7.86 ± 10.77 | 1.31× | 2.19× |
| `pathchk` | `-p` 200 operands | 1.88 ± 5.14 | 4.49 ± 13.99 | 7.50 ± 5.97 | 2.39× | 4.00× |
| `pathchk` | `--portability` one 4 KiB name | 2.48 ± 8.11 | 4.43 ± 8.51 | 5.40 ± 3.96 | 1.79× | 2.17× |
| `pathchk` | the component walk | 1.14 ± 1.40 | 2.53 ± 1.89 | 5.30 ± 3.88 | 2.22× | 4.65× |

`sum`, 2026-09-10, Linux x86-64, the same 62 MiB / 8 000 000-line file (GNU
coreutils 9.4; uutils 0.0.24 as the Debian multi-call binary):

| utility | workload | fern (ms) | gnu (ms) | uutils (ms) | gnu / fern | uutils / fern |
|---|---|---|---|---|---|---|
| `sum` | a 62 MiB file | 157.32 ± 5.17 | 133.81 ± 4.00 | 67.85 ± 11.29 | 0.85× | 0.43× |
| `sum` | `-s` a 62 MiB file | 114.01 ± 6.94 | 15.99 ± 2.10 | 21.62 ± 1.37 | **0.14×** | 0.19× |
| `sum` | a 62 MiB file from a pipe | 164.13 ± 23.18 | 153.88 ± 20.26 | 110.55 ± 17.95 | 0.94× | 0.67× |
| `sum` | `-s` a 62 MiB file from a pipe | 123.83 ± 29.78 | 32.52 ± 5.95 | 56.24 ± 15.38 | 0.26× | 0.45× |
| `sum` | a small file | 0.23 ± 0.10 | 1.14 ± 0.17 | 2.00 ± 0.17 | 4.99× | 8.75× |
| `sum` | 500 small files | 3.80 ± 0.55 | 3.95 ± 0.39 | 4.59 ± 0.60 | 1.04× | 1.21× |

The two algorithms fail differently, and only one of the failures is sum's
(#9052). BSD's rotate-then-add carries a dependency through every byte, so
neither side vectorises it: GNU's scalar loop is 2.07 ns a byte and Fern's is
2.47, and the 20% is #8425's induction variable in a stack slot. System V is
`s += buf[i]`, which gcc turns into `psadbw` — 0.09 ns a byte against Fern's
1.6 — and there is no way to spell that in Fern today, because the byte-kernel
family (`__memchr`, `__rmemchr`, `__count_byte`, `__mismatch`) has no reduction
member. Unrolling the absorb loop eight ways was measured before concluding
that and is a wash, which is what places the cost on the per-byte load.

`cksum`, 2026-09-11, Linux x86-64 (GNU coreutils 9.4; uutils 0.0.24 as
the Debian multi-call binary). The same 62 MiB / 8 000 000-line file the
digest table uses, and the `-c` workload is the same 500 small files. **The
two CRC rows and `--raw` predate the slicing-by-8 table** (#9056) and are
kept as the before side of it; the re-measure is the block under this table.

| utility | workload | fern (ms) | gnu (ms) | uutils (ms) | gnu / fern | uutils / fern |
|---|---|---|---|---|---|---|
| `cksum` | cksum of a 62 MiB file | 331.23 ± 4.03 | 14.42 ± 2.38 | 199.22 ± 3.22 | 0.04× | 0.60× |
| `cksum` | cksum of a 62 MiB file from a pipe | 336.44 ± 4.76 | 29.52 ± 4.35 | 224.19 ± 15.89 | 0.09× | 0.67× |
| `cksum` | `-a sysv` of a 62 MiB file | 114.41 ± 3.83 | 19.58 ± 6.75 | 22.15 ± 4.77 | 0.17× | 0.19× |
| `cksum` | `-a bsd` of a 62 MiB file | 163.10 ± 22.75 | 142.40 ± 16.26 | 62.44 ± 1.85 | 0.87× | 0.38× |
| `cksum` | `-a sha256` of a 62 MiB file | 578.89 ± 25.62 | 66.91 ± 7.91 | 65.20 ± 5.26 | 0.12× | 0.11× |
| `cksum` | `-a sm3` of a 62 MiB file | 560.76 ± 29.56 | 245.05 ± 8.14 | 230.44 ± 6.07 | 0.44× | 0.41× |
| `cksum` | `-a blake2b` of a 62 MiB file | 228.11 ± 13.55 | 103.72 ± 8.22 | 86.21 ± 11.13 | 0.45× | 0.38× |
| `cksum` | `--untagged -a md5` of a 62 MiB file | 241.22 ± 7.92 | 122.61 ± 3.55 | 149.09 ± 4.31 | 0.51× | 0.62× |
| `cksum` | `--raw` of a 62 MiB file | 334.59 ± 23.40 | 11.71 ± 0.80 | 200.86 ± 8.29 | 0.04× | 0.60× |
| `cksum` | cksum of a small file | 0.31 ± 0.53 | 1.69 ± 0.97 | 1.85 ± 0.34 | 5.54× | 6.04× |
| `cksum` | `-a sha256 -c` over 500 small files | 10.49 ± 2.20 | 3.68 ± 1.18 | 2.01 ± 0.63 | 0.35× | 0.19× |

**cksum does not meet the epic's bar**, and the three groups of rows fail it
for three different reasons:

- **The CRC rows are the worst in this document, and the cause is one
  instruction.** GNU's 14 ms over 62 MiB is 0.23 ns a byte, which no
  byte-at-a-time loop reaches: it folds the message with `pclmulqdq`, the
  carry-less multiply, sixteen bytes at a time. uutils' 199 ms is a table,
  so the Fern-to-uutils ratio is the codegen comparison and the ratio
  against GNU is not. `std/hash`'s `Cksum` is now a slicing-by-8 table,
  which closed the uutils half; closing the GNU half still needs the
  instruction, and `pclmulqdq` is inside the Haswell baseline — this is the
  first workload here that wants a SIMD intrinsic rather than better scalar
  code. #9056.
- **The digest rows are #8782 again**, unchanged by anything cksum does: the
  driver is the same `lib/digest.fern` the seven `*sum` utilities run, and
  raising the read block moves nothing. `sha256` is 0.12× because GNU is on
  SHA-NI, a hardware instruction, so that row is not a codegen comparison
  either; `blake2b` at 0.45× and `sm3` at 0.44× are, and they are the same
  4× gap `b2sum` measures against plain portable C.
The slicing-by-8 re-measure, 2026-09-12, **macOS arm64 (Apple Silicon), a
different machine from the table above and not comparable to it row for
row.** Same 62 MiB / 8 000 000-line file, GNU coreutils 9.10 and uutils
0.6.0, every column from one hyperfine run:

| workload | fern before (ms) | fern after (ms) | gnu (ms) | uutils (ms) | gnu / fern after | uutils / fern after |
|---|---|---|---|---|---|---|
| `cksum` of a 62 MiB file | 310.9 ± 45.7 | 199.7 ± 33.2 | 35.7 ± 0.4 | 9.8 ± 0.3 | 0.18× | 0.06× |

1.56× on the CRC, 1.62× in user time (288.2 ms against 177.6).
Slicing-by-16 was measured in the same pass and is a wash — 183.0 ms of user
time against slicing-by-8's 180.5 over 40 runs — so the table stays at eight
rather than paying for 4096 entries per hasher.

**On this host GNU's `cksum` has no hardware CRC path at all**, and that
changes what its column means. The binary links no libcrypto, carries no
`pclmul` / `vmull` / `pmull` / `neon` string, and `--debug` prints no
implementation line, so its 35.7 ms is coreutils' own generic table at
0.47 ns a byte. The 5.6× Fern still gives away to it is therefore SCALAR
CODEGEN — bounds checks, the #8425 stack-slot induction variable, #8782's
register allocation — and not a missing instruction. uutils' 9.8 ms is
0.06 ns a byte, which only the carry-less fold reaches, and THAT is the
column the `pclmulqdq` / `pmull` encodings would be for.

Ranking the three on this host, which is the honest summary: uutils (folding)
9.8 ms, GNU (generic table) 35.7 ms, Fern (slicing-by-8) 199.7 ms.

`sum -s` on the same host and the same file, one hyperfine run, before and
after `SysvSum` moved onto `__sum_bytes` (#9052). The kernel is SCALAR on
every backend here; no vector body has landed yet:

| workload | fern before (ms) | fern after (ms) | gnu (ms) | uutils (ms) | gnu / fern after | uutils / fern after |
|---|---|---|---|---|---|---|
| `sum -s` of a 62 MiB file | 110.1 ± 1.6 | 23.5 ± 0.5 | 8.6 ± 0.3 | 14.8 ± 0.7 | 0.37× | 0.63× |

**4.7×, and none of it is vectorisation** — 104.1 ms of user time against
18.1. What the kernel removed is the per-byte cost the Fern loop carried and
a runtime helper does not: a bounds check per byte, the #8425 stack-slot
induction variable, and the `SysvSum` struct rebuilt per chunk. That is worth
recording on its own, because it says how much of this family's remaining gap
is the loop body rather than the instruction set. GNU's 2.5 ms of user time
(0.04 ns a byte) is the auto-vectorised `sum += *p`, and closing to it is
what `psadbw` / `uaddlp` are for.

- **`sysv` and `bsd` are the interesting middle.** Both are a sum over every
  byte with no table, and `bsd` at 0.87× is the closest any throughput row in
  this document comes to GNU without a hardware instruction on either side.
  `sysv` at 0.17× was the outlier: GNU's is a plain `sum += *p` that a C
  compiler auto-vectorises, and the Fern loop was an indexed byte read.
  `std/hash`'s `SysvSum` now calls the `__sum_bytes` kernel (#9052), which
  is 4.7× on the macOS arm64 box measured below and still SCALAR — the
  vector bodies are a follow-up.

The startup row is the static-binary margin, widened as it is for the seven:
GNU dlopens libcrypto before it hashes a hundred bytes.

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

**`expr`'s empty alternation branch inside a COUNTED repetition follows no
branch order at all.** glibc demotes a branch that compiles to nothing — `expr
aa : '\(\|a\)a*'` reports the empty string, not `a` — and `bre.fern` now does
the same, which fixed 34 inputs. Inside `\{m,n\}` that rule stops applying and
nothing replaces it. Measured: `a : \(\|a\)\{2\}` is empty (demoted),
`\{2,3\}` is `a` (written order), `\{1,3\}` is empty, `\{1,2\}` is `a`; and
`aa : \(\|a\)\{1,2\}a*` is `a` where the same pattern without the trailing
`a*` is not. So it is not monotonic in the bound AND the rest of the pattern
decides, which means no compile-time branch order can express it.
`compile_rep` therefore compiles its body with the demotion off, leaving that
family answering exactly as it did before — 27 inputs still disagree with GNU,
all of them this shape, all pre-existing. #9092 §7 has the measurements.

Applying the demotion uniformly was tried and rejected: it fixed 8 more inputs
and broke 6, which is a worse trade than leaving a known shape alone.

The other six shapes in #9092 are GNU behaving worse than Fern — a SIGSEGV on
`expr "" : '\(\)\1\{2\}*'`, a non-terminating match on
`expr bba : '\(b\|\|a\)\?*'`, and four no-matches where Fern finds one.
Those are deliberately NOT reproduced.

**`mkdir -Z` and `mkdir --context[=CTX]` on a kernel that HAS SELinux.** GNU
sets a security context and no primitive here can, so the option is refused —
`setting a security context is not supported on this system`, exit 1 — rather
than quietly ignored, which is the answer `runcon` gives for running a command
in a context. No machine the corpus runs on has SELinux, so the compared path is
the one where GNU warns (`--context=CTX`, once per occurrence) or says nothing
(`-Z`, a bare `--context`) and carries on.

**`chcon` refuses the context change itself, and there is no answer that
would not.** A security context is an extended attribute; there is no
`getxattr` or `setxattr` (#9098), so the one step the utility exists for
cannot happen. What makes this a divergence rather than a gap is that GNU
does not refuse either: on a machine with no SELinux it calls
`setfilecon(3)` and reports whatever the call failed with, and WHICH errno
that is belongs to the build and to the caller rather than to chcon —
`Operation not supported` from gnulib's stub where coreutils was configured
without libselinux, `Operation not permitted` from the kernel where it was
configured with it and the caller may not write `security.*`. No Fern binary
can predict that byte. So GNU's frame is kept and our own sentence sits in
the errno slot: `failed to change context of 'f' to 'ctx': setting a
security context is not supported on this system`, exit 1, nothing changed
— which is the same outcome in kind, since on such a machine GNU changes
nothing either. `--reference` and the `-u -r -t -l` component form need the
READ side of the same attribute and are refused the same way.

The corpus is therefore the 96 invocations that never reach the call: the
whole option grammar, the two `-R` traversal combinations GNU rejects
outright, the operand counts, `cannot access`, `cannot read directory` —
reachable with a real directory, because fts reports it INSTEAD of yielding
the visit the change hangs off — and every spelling of the root failsafe.
`conflicting security context specifiers given` is unreachable on such a
machine: GNU checks it AFTER reading the reference file, which has already
failed. `chcon.fern` keeps that order rather than tidying it.

**`mkdir`'s post-creation chmod failing is the one wording in the utility the
reference binary has never been made to print.** A directory this process just
created is one it owns and may chmod — a setgid one in a group it does not
belong to included, measured as an ordinary user against a setgid parent — so
the path looks unreachable and `cannot change permissions of 'NAME': <strerror>`
is unverified. It is flagged as a guess in a comment at the call site.

**`mkdir -p`'s "can I enter this ancestor" check is `stat` of `dir/.` where
GNU's is a `chdir`.** The two answer identically for every errno the corpus
reaches — success, EACCES, ENOENT, ENOTDIR, ELOOP, measured as root and as an
ordinary user — but the name asked about is two bytes longer, so a path within
two bytes of PATH_MAX could answer ENAMETOOLONG where GNU's would not. Fern has
no chdir builtin, and one was judged too narrow a reason to add it.

**`df --sync` does not sync.** GNU calls `sync(2)` before it measures, so its
numbers are post-writeback. Fern has no such builtin — no `sync`, `fsync` or
`syncfs` in FuncSigs, and no flush-to-device on `Writer` — so `df.fern` accepts
the option and does nothing with it, which is what `--no-sync` does. The corpus
cannot see it: the only filesystems whose numbers are stable enough to diff
between the GNU leg and the Fern leg are the ones reporting no blocks at all,
and flushing changes nothing there. #9089 carries the four sync calls and the
classifications they need; #9102 was filed separately for this caller and is the
same builtin.

**`df` is Linux-only.** `statfs` answers the counts on every native target, but
not the device, the mount point or the type NAME, and those come from
`/proc/self/mountinfo` — which is what GNU reads too, visible as one `openat`
under strace. Darwin has no such file, so an `arm64-darwin` build compiles and
then fails on its first line with `cannot read table of mounted file systems`.
Darwin answers all three from `getfsstat(2)`, whose `struct statfs` carries
`f_mntfromname`, `f_mntonname` and — the part Linux makes hard —
`f_fstypename`. #9104 is that primitive, shaped as a list rather than a lookup
because df deduplicates by device across the whole table.

**`stat` cannot report a birth time, a file's SELinux context, or three of
statfs's fields.** `stat` and `lstat` lower to `newfstatat(2)`, whose `struct
stat` has no birth time (#9096); a file's context is an extended attribute and
there is no `getxattr` (#9098); and `statfs` reads `f_type`, `f_fsid` and
`f_frsize` and then drops them (#9097). That is `%w`, `%W`, `%C` and `-f`'s
`%t`, `%T`, `%i`, `%S` — and the DEFAULT multi-line block, `--terse`, `-f` and
`-t -f` all carry one of them, so all four are refused with a diagnostic naming
the field and exit 1. The refusal comes only after the operand has been read,
so `stat nosuch` still reports `cannot statx` exactly as GNU does.

Printing GNU's own "unknown" rendering instead — `-` and `0` for a birth time,
a zeroed magic number — was the tempting shape and is the one thing that must
not happen: ext4 on every machine the gate runs on DOES report a birth time,
so those bytes would be an invention and the corpus would be measuring it. The
corpus therefore holds 414 cases over the format engine and none over the four
layouts; they arrive with the primitives, and #8366 stays open until they do.

`QUOTING_STYLE` is the second, smaller gap: GNU takes `%N`'s quoting style from
it and `stat.fern` always uses the default shell-escape-always. It reaches `%N`
and nothing else — the default block prints the name literally whatever the
variable says. #9105 has the measurement and puts the remaining gnulib styles
in `lib/gnu.fern`, where `ls` will want them too.

**`mv` does not copy across filesystems.** GNU falls back to a recursive
copy-then-unlink when `rename(2)` answers EXDEV. Measured, it preserves mode
including setuid, both timestamps on files AND directories, ownership, symlinks
as symlinks, FIFOs as FIFOs, and hard-link structure within one invocation; it
UNLINKS an existing destination rather than truncating it, and a partial failure
leaves the source intact. `mv.fern` reports the errno instead, which is exactly
what GNU's own `--no-copy` does with it — a loud failure rather than a wrong
result. Smallest divergence: `mv -v a /dev/shm/c` from an ext4 directory, where
GNU prints `copied` then `removed` and exits 0.

Closing it needs more than #9085 delivered, which is worth stating because it
looked otherwise: besides `chmod` and `set_file_times`, both lowered now, it
needs `chown`/`lchown` and `mkfifo`/`mknod`, and neither exists as a builtin in
either compiler. #9089 carries all four. The corpus reaches EXDEV for real
through the harness's `crossDev` field — `/dev/shm` is tmpfs where `/tmp` is
ext4 — but only under `--no-copy` / `-n` / `--update=none`, which prove the
errno is reported identically.

**`du` cannot walk past `PATH_MAX`.** Every filesystem primitive takes a PATH,
so a component 8 KiB down is `File name too long` where GNU's fts, which opens
each directory and reads it fd-relative, keeps going. Measured on a 40-level
tree of 200-byte components: GNU 168, Fern 80 plus one `cannot access` per
level it could not reach, exit 1 — the same shape an unreadable directory has,
so it degrades rather than lying. #9078 and #9074 are the same `openat` /
`fdopendir` / `fstatat` family, which `rm -r`, `ls -R`, `find` and `cp -r` all
want too.

**`fmt` formats a whole paragraph where GNU formats 997 words at a time.**
GNU fills a fixed word buffer, lays out what it holds, and re-enters from the
remainder, so a paragraph past that bound is laid out from state the re-entry
leaves behind — 38 words come back as [36, 2] after a flush and as [35, 3] when
they are a paragraph of their own, and nothing in the streams identifies which
happened. `fmt.fern` formats the paragraph. The first divergence is 999
one-character words at `-w 75`; nothing under 998 differs, and the corpus holds
a 996-word paragraph and none above it.

**`dircolors -p` prints GNU 9.4's database.** The text `-p` prints is a data
file that changes between coreutils releases — the copyright year on its third
line moves, and entries come and go — so there is no version-independent answer
to print. `coreutils/dircolors.fern` carries GNU 9.4's, transcribed from that
binary's own `-p` output (the file grants permission to copy and distribute it
with its notice preserved, which is why it can be carried at all). `-p` and
every invocation with no FILE read that text, so against a newer oracle those
cases fail loudly rather than passing something wrong, as `uname -p` does on an
unpatched distribution; the fix is to transcribe the newer `dircolors -p`.
Nothing else in the utility is version-sensitive.

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
**`who` and `pinky` under a TZ that names daylight time but no dates.**
`TZ=ABC1DEF` — a zone name, an offset, a daylight name and nothing more —
leaves the changeover dates to the implementation, and so does a TZ whose
dates are malformed (`TZ=EST5EDT,J0,J300`). glibc reads them from
`/usr/share/zoneinfo/posixrules`, a copy of America/New_York that upstream
tzdata stopped shipping in 2020 and Debian still carries as a symlink: it
applies that file's transition times with the TZ string's offsets
substituted and a correction that moves the spring change by the
difference between the two standard offsets, and past the file's own table
(2037) it abandons the TZ string entirely — `TZ=ABC1DEF date -d @2147483647`
prints `EST -0500`, not the `ABC` the string names. `lib/tz.fern` applies
the POSIX default dates instead, the United States rule in force since
2007, which is what glibc itself uses on a system with no posixrules file.
The two agree on every date between those dates and 2037 and differ
outside it; the corpus therefore carries no case of that TZ shape, and
`who_test.go` says so where a reader will meet it.

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

**`runcon` cannot run a command.** Everything a kernel without SELinux reaches
is byte-exact — the current context with no operands, `no command specified`,
the `may be used only on a SELinux kernel` refusal, and the whole non-permuting
`+r:t:u:l:c` scan including the `multiple roles` family and the `--r` ambiguity
list. Past the SELinux check nothing is implemented, because three pieces are
each blocked on a primitive: `execvp(3)` passes the command NAME as the child's
argv[0] where `proc_exec` forces the resolved path it was handed; `-c` needs
`getfilecon(3)`, an extended-attribute read with no primitive at all; and
`security_check_context(3)` reads the kernel's verdict back off the descriptor
it wrote the context to, which `write_file` cannot do. So a build on an SELinux
host says `running a command in a security context is not supported on this
system` and exits 125 where GNU would run the command — the shape `split
--filter` already uses — with every option still declared, since their getopt
behaviour is observable either way.

## Open gaps

**Accumulating into an array or a string is quadratic under the self-host
compiler (#9077).** `xs = xs.append(v)` and `s = s + piece` grow in place under
native and copy per step in the self-host build, so a corpus cannot hand the
self-host leg a large accumulation: `dircolors`' large-input cases are sized to
what the self-host finishes (6000 entries — past a read block on the way in and
past a pipe buffer on the way out) rather than to what native would take. Found
when the self-host build of dircolors was SIGKILLed on a case native finishes
in 0.19 s.

**A directory walk is bounded by PATH_MAX (#9074).** Every filesystem builtin
takes a path, so a recursive walk concatenates one per entry and the kernel
refuses it past 4096 bytes. GNU's fts is fd-relative (FTS_CWDFD: openat /
fdopendir / unlinkat against a held descriptor) and has no such bound: on a
tree built with fd-relative mkdir to 80 levels of 100 characters, `rm -rf`
removes it in silence while `rm.fern` stops at level 40 with `File name too
long` and leaves the rest. Shallow ENAMETOOLONG is an ordinary error path both
sides agree on. `remove_dir_all` is fd-relative already but offers no
per-entry hook, so it cannot carry `-v`, `-i`, or the per-entry diagnostics
that decide the exit status. `du`, `ls -R`, `cp -r`, `chmod -R` and `find`
want the same primitive.

`chmod -R` is the second utility standing on it, and there the gap costs more
than depth. strace shows GNU descends fd-relative below the top level —
`fchmodat(4, "sub", …)` against a held descriptor, then `openat(4, "sub",
O_NOFOLLOW|O_DIRECTORY)` — so renaming an interior directory under a running
walk cannot redirect a chmod at anything outside the tree. `chmod.fern`
rebuilds the path per entry, so it can. Nothing in the corpus renames anything
under a running chmod, and the 333 cases agree on stdout, stderr, exit status
and the mode of every entry; what is missing is a safety property no case
asserts, which is why it is recorded here rather than left to the depth
sentence above.

`chown -R` and `chgrp -R` are the third and fourth, with the same shape: they
rebuild the path per entry where GNU holds a descriptor, so a rename of an
interior directory under a running walk can redirect a call outside the tree.
Nothing in the corpus renames anything under a running chown.

**`chown -v` on a dangling symlink it was told to FOLLOW is the one place the
corpus deliberately does not compare stdout, and the reason is that GNU has no
answer to compare against.** The `-v` line carries a "from" clause built out of
a `stat` buffer the failed `stat` never filled: on one machine the same broken
link reported `from wheel`, `from 2` and `from _uucp:wheel` across runs of
different binaries, against a link that was really `jakechampion:admin`. There
is no value a second implementation could print that would match, so no case
pairs `-v` with that combination. The stderr line (`cannot dereference 'X': No
such file or directory`) and the exit status ARE deterministic, and cases
compare both; `-h`, which succeeds on a broken link, is compared with `-v` in
full. `chown.fern` fills the clause from the `lstat` it already did, which is
the link's real ownership.

**`chgrp --from=` and `chgrp`'s refusal of `(gid_t) -1` are 9.10 behaviour that
9.1 does not have, and neither is in the corpus.** Measured on both: 9.1's
chgrp answers `unrecognized option '--from='` and accepts `4294967295` as a
gid, where 9.10 takes the option and answers `invalid group: '4294967295'`.
chgrp reached GNU's shared `parse_user_spec` somewhere between the two. The
corpus is held to "9.4 or newer" and a case may only assert what every version
in that range does, so these two are implemented to 9.10 and left uncompared
rather than pinned to a version the runner may not have. `chown` is unaffected
— it has had both since long before 9.1, and its cases cover them.

**Three rm paths are outside the corpus.** `--one-file-system` and
`--preserve-root=all` only act across a mount point and the harness cannot
mount one, so they stand in the corpus as the inert invocations that prove
they parse; both were compared against GNU over a real tmpfs by hand,
including the `--preserve-root=all --no-preserve-root` order in which GNU
keeps the device check and drops only the `/` failsafe. The write-protected
prompt needs a file the test user cannot write, which as root does not exist;
it was compared under uid 65534 by hand, and in the corpus it stands as the
`---presume-input-tty` cases, which agree either way. And GNU carries a
fourth prompt wording, `attempt removal of inaccessible directory %s? `, that
no directory mode from 000 to 555 could reach — the FTS_DNR path answers
first — so rm.fern does not have it.


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

**A failed getcwd carries no errno (#9067).** `getcwd()` is a bare string and
reports failure as the empty one, where every other filesystem builtin is a
`Result[T, IoError]` carrying glibc's strerror text. `readlink -f` and
`realpath` on a RELATIVE operand start at the working directory, and GNU names
what getcwd(2) said when it will not answer — `No such file or directory` for a
directory that has been removed, `Permission denied` for one whose ancestor lost
search permission. `lib/canon.fern` can report only the first, and does. The
corpus cannot reach either: the harness has no way to put both children in the
same removed or unsearchable directory.

**`read_file` reads to EOF rather than trusting `st_size` (#9065, fixed).** It
used to size its buffer from `fstat` and report the string's length as
`st_size`, so every `/proc` file read empty and every `/sys` file read as a page
of mostly NUL — and the interpreter disagreed, since Go's `os.ReadFile` grows.
Four readers were silently wrong on that: `id`'s selinuxfs probe always answered
false, `id`'s own context came back empty, `logname` never saw
`/proc/self/loginuid`, and `nproc`'s `/proc/cpuinfo` fallback always counted
zero CPUs. Fixed in all four backends and the self-host, so a caller needs
nothing between it and the kernel. A regular file still allocates once.

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
  `paste`, `join`, `comm`, `uniq`, `sort`, `tr`, `fold`, `fmt`, `expand`,
  `unexpand`,
  `pr`, `ptx`, `split`, `csplit`, `shuf`, `od`, `base32`, `base64`, `basenc`,
  `sum`,
  `tee` and the seven checksum utilities. `tee` wanted signal dispositions (#8792) for `-i`
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
  `whoami` `id` `groups` `logname` `users` `who` `pinky` (done — the
  family needed NO new primitive: utmp is `read_file_bytes`, who's
  message-status and idle columns are `stat`, and the local timestamp
  `who` and `pinky` print is `read_file` plus `env`, which is
  `lib/tz.fern`), `printenv` (done) `env` (the whole
  environ, exec), `ln`
  (link, symlink, readlink; `link`, `unlink`, `readlink` and `realpath`
  are done on `read_link()` from #8883, leaving `ln`),
  `mkdir` `rmdir` `rm` (done) `mv` `cp`
  `install` `touch` `truncate` `mkfifo` `mknod` `sync` (rename,
  utimensat, ftruncate, mknod, fsync; `mkdir` with a mode and `rmdir` are
  primitives now. `rename`, `chmod` and `set_file_times` landed natively
  with #9059; `rename`, `chmod` and `set_file_times` have since reached
  the self-hosted COMPILER too — see the paragraph below, and #9085), `mktemp` (done — it needed none of them: `open_exclusive`,
  `create_dir`, `remove_dir`, `remove_file`, `lstat`, `random_bytes` and
  `env` were all already here, so its banner was stale), `chmod` (done),
  `chown` `chgrp` `runcon`, `chcon` (done — the option grammar, the walk and
  every diagnostic before the context change; the change itself has no
  primitive, see the divergence above), `stat` `ls` `dir` `vdir` `du` `df`
  (full stat, statfs, d_type), `dircolors` (done — it needed none of
  those: `env()` for $SHELL / $TERM / $COLORTERM and no new primitive), `date` (strftime; the timezone half is `lib/tz.fern` now), `timeout` `nice`
  `nohup` `kill` `stdbuf` `chroot` (signals, setpriority, exec), `dd`
  `shred` `stty` `uptime`, `pathchk` (done — it needed no new primitive:
  `lstat` is the whole of the default mode and `statfs` from #9062 carries
  the per-directory `name_max` its component walk holds a name to, which is
  exactly the caller that builtin's own doc comment predicted; `pathconf` /
  `statvfs`, which #8384 lists as the blocker, are not needed), and `hostid`
  (done: `hostname()`
  plus the resolver in `lib/resolv.fern`). The
  sub-issue for each utility names the primitives it is blocked on; the
  primitive gets its own issue when the first utility needs it.

  **What a primitive costs, and the half that has no gate.** A builtin is
  four classifications — `internal/checker`, `internal/interp`,
  `internal/caps` (`docs/PACKAGE-CAPABILITIES-BRIEF.md`) and
  `internal/platforms` (`docs/FREESTANDING-CORE.md`) — plus the two
  self-host MIRRORS, `examples/self_host/caps.fern` and `platforms.fern`.
  Each of those has a completeness test that fails when one is missed.

  It is also, and this is the expensive half, the self-hosted COMPILER:
  `parser.fern`'s name list, `ircore.fern`, `ir.fern` (op + extension kind
  id), `irlower.fern`, `asmcore.fern` and the three emitters. **Nothing
  tests for that.** `sleep_ns` was classified in all six places, never
  lowered, and #9060 merged with the self-host leg of `sleep` red; main
  stayed broken until #9081. So a primitive is not landed until the
  self-host compiles a program that calls it, and the utility's parity
  suite covers its self-host leg. #9085 tracks the missing completeness
  test. **All of #9085's five are lowered now**, and the completeness test
  landed with no exemption list: `TestSelfHostKnowsEveryNativeBuiltin` in
  `internal/checker` pins every bare builtin the checker registers against
  `builtin_function_names()` in the self-hosted parser, and fails naming
  each one that is missing. Lowered: `sleep_ns` (266), `rename` (267),
  `chmod` (268), `set_file_times` (269), `statfs` (270), `process_alive`
  (271), `rlimit_nofile` (272), `truncate` (273), `mknod` (274).

  `truncate` is path-based rather than fd-based, and the reason is
  measured: `open_writer` is `O_WRONLY|O_CREAT|O_TRUNC`, `open_appender`
  creates too, and `open_exclusive` fails on a file that exists — so no
  Fern open yields a writable descriptor to an EXISTING file without
  first emptying it, and an `ftruncate` form could not express
  `truncate -s +10 file`. It does not create: that is `open_exclusive`
  followed by this. Neither WASI preview has a path-based set-size —
  `path_filestat_set_size` is not a preview-1 import, measured against
  wasmtime rather than read from a header — so both wasm bodies open
  without CREATE or TRUNCATE, set the size through the descriptor and
  drop it.

  `mknod` is ONE builtin for both `mkfifo(1)` and `mknod(1)`, since
  `mkfifo(3)` is this with a fixed type. It is refused on wasm under a
  new capability `fsnode`: what a wasm host lacks is the KIND, not
  permission bits, and a regular file standing in for a FIFO is worse
  than an absent entry rather than better — a program that opened it
  would block forever on a read a pipe would have answered. Its major /
  minor pair is Fern's own and each backend packs the target's `dev_t`,
  because Linux SPLITS the minor around the major (minor[7:0],
  major[11:0], then minor[19:8] from bit 20) and XNU does not. That
  layout came from creating nodes with `mknod(1)` and reading `st_rdev`
  back: the legacy 8+8 encoding agrees for every pair that fits in a
  byte each, so `(1,3)` and `(255,255)` cannot tell them apart and
  `(1,256)` can. A second measurement corrected the range — the
  12 + 20 bit ceiling is the ENCODING's, not the kernel's, since
  `mknodat` ignores every bit of `dev` above 31 — so an out-of-range
  pair would land silently on a DIFFERENT valid node, and every backend
  substitutes an S_IFMT no file type uses to get the kernel's own EINVAL
  rather than inventing one.

  The third primitive #9089 asked for, `sync`, is NOT landed and its
  shape is disputed. `syncfs` has no XNU equivalent, and
  `internal/platforms` grants capabilities per PROFILE with linux,
  darwin and android all naming `hosted-native` — so the first
  Linux-only capability needs a per-environment split, which is a
  platform-layer decision rather than something a primitive should
  decide on its way past. And `fsync` / `fdatasync` are fd operations:
  this codebase's own rule is that authority lives on the constructor
  and not the method, which is why `Reader.stat` is a method and takes
  no capability, so those two belong on `Reader` / `Writer` beside it
  and need neither a capability nor a builtin name. #8360 stays blocked;
  #9102 is the same builtin filed a second time.

  That test covers the half that fails SILENTLY — an unknown name is E001
  at the call site, which reads like the program's mistake rather than the
  compiler's. The lowering half is self-reporting by comparison: a name
  that reaches the parser with no IR op behind it stops at a diagnostic
  naming the bail site.

  Two of those do not reach every target, and the refusal is deliberate
  rather than a gap: `chmod` is refused on wasm (E066, capability
  `fsmode` — neither WASI preview has permission bits), and
  `process_alive` and `rlimit_nofile` are refused there too, named by
  the wasm emitter rather than by the platforms gate, because
  `wasm_ir_run` / `wasm_run` / `playground_run` reach `emit_ir_module`
  directly and would otherwise present a deliberate absence as a
  missing lowering — and `statfs` is refused there too, for the reason
  `internal/interp/fsstat_other.go` gives: neither preview has a volume to
  measure, since a preopen is a capability handle rather than a mount, so
  it reports neither a size nor a name-length limit. On arm64-darwin
  `set_file_times` issues
  `setattrlist(2)` where native issues `setattrlistat(2)`: the
  self-host's syscall floor stops at five arguments and `setattrlistat`
  takes six, and the two are the same call for a path resolved against
  the process's own directory, which is what `AT_FDCWD` means.

Within a group, easiest first. Do not start a group-C utility by adding a
one-off syscall to one backend.
