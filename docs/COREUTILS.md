# Fern coreutils

`coreutils/` reimplements GNU coreutils in Fern, one program per file, to two
requirements that do not bend:

1. **Byte-for-byte parity with GNU coreutils 9.12.** Same stdout, same stderr,
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

A case CANNOT name a starting niceness, and the reason generalises to any
process-global state the harness cannot put back. `nice` with no COMMAND
prints the niceness it was started at, and the 0 the suite inherits is
exactly the value a broken read produces by accident — Linux's `getpriority`
answers the nice value BIASED by 20, so a helper that forwards it says 20
where the truth is 0. But RAISING the harness process's own niceness is
one-way for an unprivileged runner: lowering it back needs privilege, so the
restore fails, the value ratchets, and the self-host leg's parallel cases read
each other's. (`PRIO_PROCESS` is a misnomer too — the value is per-THREAD on
Linux with no per-process form — so even the set does not reliably reach the
child.) The starting value comes from a WRAPPER instead: GNU `nice` raising it
for a child, which needs no privilege and dies with the child. See
`TestNiceReadsTheNicenessItWasStartedAt`.

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

### The four exemptions

Four outputs are ours by design, because their content names the
implementation:

- `--version` prints `<util> (Fern coreutils) <version>` and nothing else.
  Claiming GNU's version string would make the one output that identifies
  the program lie about which program it is.
- `--help` is our own text. GNU's is GPL-licensed prose carrying GNU's URLs,
  authors and (in 9.x) terminal hyperlink escapes; reproducing it would be
  copying, and it would be wrong in every particular that matters.
- `cksum --debug` — "indicate which implementation used" — is silent. GNU's
  two CRCs have several implementations each and it picks one at startup by
  asking the CPU (`using pclmul hardware support` where the instruction
  exists), which is a runtime dispatch a static Fern binary with no CPU
  detection does not have; claiming the message would say something untrue
  about our own code, and printing a different one would diverge just the
  same. Only `crc` and `crc32b` reach it — GNU says nothing under `--debug`
  for the other twelve algorithms, and neither do we, so those ARE in the
  byte-exact corpus, as is everything else about the option: that it is
  accepted, that it refuses a value, and that it stands in the ambiguity
  list.
- `cp --debug`'s second line is ours, for the same reason as `cksum
  --debug`'s and no other. GNU's names ITS OWN syscall strategy —
  measured, `copy offload: yes, reflink: unsupported, sparse detection:
  no` for a dense file and `copy offload: unknown, …, sparse detection:
  SEEK_HOLE` for a sparse one — which is `copy_file_range` offload and
  `SEEK_HOLE` probing, neither of which has a Fern primitive. Claiming
  either would say something untrue about our own code. Ours states what
  the copy actually did, and the corpus holds `--debug` to its exit
  status and stream rather than its bytes. Everything else about the
  option IS byte-exact: that it implies `-v`, that the `'src' -> 'dest'`
  lines it implies are identical, that a directory and a FIFO draw no
  such line while a regular file does, and that it stands in the
  ambiguity list between `--copy-contents` and `--dereference`.

  This one has a way out that `cksum --debug` does not: a
  `copy_file_range` / `SEEK_HOLE` primitive would let the line be true
  rather than ours. Until then it is an exemption, not a divergence to
  fix in cp.

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
stops, a byte limit, and a pseudo-terminal on any of fds 0, 1, 2 or 3); the
harness runs GNU and Fern and diffs. A case costs one line, and a case cannot
record a wrong expectation, which is what makes the corpus cheap to grow and
hard to get wrong. See the package doc in `harness_test.go`.

**A pseudo-terminal is the only way to reach the isatty half of a utility**,
and until `ttyIn` / `ttyOut` / `ttyErr` the harness could put one on fd 3
alone — enough for `test -t 3` and nothing else, because fds 0-2 were pipes
for every case. Each of the three now replaces its descriptor with the slave
side of its own pty, with the master drained by a goroutine (a terminal holds
a few kilobytes, so a child writing more than that into one nobody reads
deadlocks) and the window size set to 24x80 rather than left at the 0x0 a
fresh pty carries, so a layout is pinned by the case and not by a fallback.
One pty per descriptor rather than one shared: a real console gives fds 1 and
2 the same terminal, but the harness compares the two streams separately and
a shared one would interleave them.

Two consequences, both of which BOTH sides meet: the line discipline turns
each `\n` into `\r\n` on the way out, so a terminal case's bytes carry the
`\r`; and it is Linux-only, because the slave is reached through `TIOCGPTN`
and Darwin needs `grantpt` out of libc. That is the line `/dev/full` is
already on.

It found five `ls` bugs on the first run, which is what a gate with no oracle
looks like from the other side. `ls` alone — not `dir`, not `vdir`, measured —
changes three defaults together when standard output is a terminal: vertical
columns instead of one entry per line, shell-escape quoting instead of
literal, and nongraphic characters shown as `?`. A terminal that answers
`TIOCGWINSZ` also ENDS the width question rather than merely outranking
`COLUMNS`: `COLUMNS=abc ls` warns down a pipe and says nothing on a terminal.
The other two were reachable down a pipe all along and nothing had asked: a
hyperlinked name wearing outer quotes leaves them OUTSIDE the link under the
padding regime (`-C`, `-x`, `-l`, where the quote is what the bare names'
leading space lines up with; `-m` and `-1` keep both inside), and `--dired`
is DROPPED when hyperlinking, because its byte offsets would name positions
inside the escape sequences.

**The reference is a BINARY, not a directory.** The harness chooses one
directory by probing `yes --version`, but it then verifies the utility's own
binary inside it and looks in the remaining candidates when that one is not
GNU coreutils. That is not caution for its own sake: `/usr/bin/uptime` on
Debian and Ubuntu belongs to **procps**, because their coreutils package does
not build coreutils' own — nor `kill`'s. Trusting the directory would have
compared Fern against a different program and reported the difference as
Fern's bug. `FERN_GNU_COREUTILS` therefore takes a PATH-style LIST, so a host
supplies what its distribution leaves out beside `/usr/bin` rather than
instead of it, and `scripts/devbox` and the `test-units` lane both build the
one missing binary and point at it. A reference that cannot be found is a
FAILURE naming the utility, never a skip.

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

That shared directory is a fresh temporary one per case unless the case names
a `dir`, so a utility that creates a file named by an operand cannot write it
into the source tree. Name a `dir` when the case is ABOUT the directory: `pwd`
reached through a symbolic link, where the logical and physical answers
differ, or an operand that has to be spelled relative to a tree the case
built.

The reference is whatever GNU coreutils the harness finds:
`$FERN_GNU_COREUTILS`, then the `yes` on PATH if its `--version` says GNU,
then the fixed system paths, then a nix store glob. **Not finding one is a
failure, not a skip.** On the Ubuntu CI runners it is the system coreutils;
on macOS the system tools are BSD, so a nix or Homebrew GNU coreutils is
needed and the failure message says so.

Versions: the corpus is held to GNU coreutils **9.12**, and that is enforced
by supplying the reference rather than hoping for it.
`scripts/devbox`'s base is `debian:bookworm`, whose coreutils is **9.1** —
below the floor, and the container spent its life comparing against it, which
is not a gate (#9162). The image now builds 9.12 and puts it ahead of
`/usr/bin`, so a green run in the container means what a green lane means.
That also supplies the two binaries Debian does not build at all, `uptime`
and `kill`, and asserts both rather than only the first. CI's `test-units`
lane installs the same 9.12 tree the same way, AHEAD of `/usr/bin` rather
than beside it. Installing only the binaries the image lacks is what this
looks like when it goes wrong: the lane did that, with `/usr/bin` first in
the list, so sixty-odd utilities compared against ubuntu-24.04's **9.4**
while the corpus encoded 9.12 — around two thousand cases reporting a
version difference as Fern's bug. The harness takes the first directory on
the list that holds the utility, so a system coreutils in front of the built
one shadows every name it also carries.

The `macos-15` lane (`.github/workflows/macos.yml`) builds the same 9.12 and
installs the WHOLE tree to `~/gnu-coreutils`, because there is no system GNU
on that runner to fall back to for the rest. One program needs naming: `arch`
is an automake EXTRA_PROGRAM in coreutils' `no_install__progs`, so `make`
alone builds nothing to copy and `make install` places every other program
and never it. Every lane passes `--enable-install-program=arch,kill,uptime`
for that reason. `/usr/bin/arch` on macOS is Apple's unrelated arch(1), which
the harness's version probe rejects. That lane runs the corpus for the whole
catalogue with `-skip '^TestSelfHost'`:
selected by skipping rather than by naming, because `touch_test.go` and
`ls_linux_fixture_test.go` are `//go:build linux` and a `-run` list carrying
`TestTouch` would select nothing there, silently and with exit 0. Benchmarks
compare against both GNU coreutils and Rust uutils, recording their actual
versions. Install missing comparison implementations before measuring.
A case whose behaviour changed between versions records the version it needs in a
comment and is the exception, not the pattern. #8765 was the standing example —
`numfmt`'s buffer-length refusal is GNU <= 9.4 behaviour — and it is settled:
Fern follows 9.5+, the refusal is gone from the scaled path, and the corpus
compares those rows against 9.12 like any other. The unscaled
`value/precision too large` limit is a different rule and 9.12 still applies
it, so only half of that issue's surface moved.

**The benchmark uses the same tree.** It did not always: while the corpus was
pinned to 9.4 the bench had its own 9.12 so "faster than GNU" meant the GNU
people run. Now that both are 9.12 there is nothing to differ about, and
`scripts/coreutils-bench` reads `FERN_GNU_COREUTILS` like everything else. It
still carries the release as `bench_gnu_floor` and says on stderr when the
tree it found is older, warning rather than exiting: the bench is a
comparison and not a gate. Two utilities always answer from elsewhere —
`chcon` and `runcon` need SELinux and the built tree configures
`--without-selinux` — so the directory on PATH is appended to the search list
after the named ones, and a utility answered from there names its real
version on stderr.

### The Darwin ratchet

Running the whole catalogue on macOS surfaces 1,887 failing cases over 19,913
tests, across 83 utilities — divergences the Linux lanes never see, because
Darwin has no `/proc`, a different `struct stat`, a different `getgrouplist`,
and a case-insensitive filesystem by default. Holding the lane to zero would
leave it red indefinitely, so it is gated by a ratchet instead:
`.github/darwin-corpus-known-failures.txt` names the test functions allowed
to fail, and the lane fails when a name NOT on that list fails. Entries only
ever leave the file — a utility that starts failing is a regression to fix,
never a line to add. The case counts behind those names and the order worth
working them in are #9714.

That rule has been broken exactly once, and the reason is why the gate looks
the way it does. The list was first seeded at 51 names from a run reported as
complete which had executed 14,814 of 19,913 tests: `numfmt --padding` with a
20-digit width is a valid invocation GNU answers with about 10^20 spaces, the
harness captured it unbounded, and the resulting memory pressure killed tests
running beside it. Every file from `pwd_test.go` onward went unmeasured, so 32
utilities were recorded as passing without ever having run, and the first
round after the capture was capped reported all 32 at once — as regressions.
They were not: their causes are a `strerror` table carrying one message text
per errno across all three platforms, an unimplemented `utmpx`, and filename
fixtures APFS will not create. So the list was re-seeded to 83 from two
independent complete runs that agreed byte-for-byte. A re-seed is a repair of
a baseline that was never measured, not a licence to record a regression.

The gate reads the corpus log rather than the corpus step's exit status: that
step deliberately does not propagate it, because the whole point is that a
failing corpus is not by itself a failing lane. It insists on a `DONE` line,
and then on the test count in that line clearing the `# min-tests:` floor the
ledger carries. The count is the stronger of the two: a runaway case takes the
tail of the run down with it and still leaves a `DONE` line behind, and the
short run's narrower failure set then reads as an improvement rather than as
a measurement that never happened. A run below the floor is refused. Lower the
floor only when the catalogue shrinks on purpose; raising it to clear a failure
is the same mistake in a new place.

A name on the list that PASSED is a `::warning::`, not an error. That is a
deliberate deviation from the two-way ratchet in `internal/lint`, where a
number moving in either direction fails: `ptx` failed on one round and passed
on two, so erroring in that direction would make the lane flaky on a utility
nobody had changed. The warning is still the prompt to delete the line.

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

### The wasm leg

`TestWasmSmoke` compiles a handful of utilities for `wasm32-wasi`, runs them
under wasmtime and holds them to the native build of the same source. It is
not a parity gate — a dozen invocations, not a corpus — but before it nothing
ran the tree on the wasm target, and the first run found three faults in the
runtime rather than in any utility (#9070): `exit(n)` for `n > 1` trapped the
host because `wasi:cli/exit` carries one bit, a stdio handle answered `stat`
with Unsupported where a preview-1 host answers the all-zero record, and a
path with no preopened directory trapped on a handle the host never issued
instead of answering NotFound. Two things the leg does not compare by value:
the exit status, which the host folds to 0 or 1, and anything under a
directory the guest was not given. Note for anyone running a utility by hand:
`wasmtime run util.wasm -- --version` hands the `--` to the guest as
`argv[1]`, which every utility rightly takes as the end of its options.

Not every utility can be in the leg. `shred` does not BUILD for
`wasm32-wasi`: `-f` makes an unwritable entry writable, which is `chmod`,
which needs `fsmode` — a mode word no wasm host has — so E066 refuses it
post-tree-shake. That is the same answer `sort --batch-size` gets, and for
the same reason: the alternative is a `-f` that silently does not force.
Writer.seek, the primitive shred is built on, is covered on both previews
by `internal/e2e/handle_stat_seek_test.go` instead. `dd`, built on the same
seek and reaching no mode word, does build and is in the leg — the one
utility there that WRITES a file, so the leg covers a preopened
directory's `path_open` and the write loop behind it.

`ls`, `dir` and `vdir` do not build there either, and for two capabilities
rather than one: `tty`, because the default format, the default quoting and
the width all ask whether standard output is a terminal, and `cwd`, because
`--hyperlink` names the canonical path. Both are
features of the utility rather than incidental — a `window_size` that
answered 80 would claim a measurement, and a `--hyperlink` that emitted a
relative URI would be wrong — and E066 refuses them post-tree-shake for the
same reason it refuses `shred -f`. They join `pwd`, `readlink`, `realpath`
and `stat`, each of which reaches `getcwd` the same way.

Neither does any COMMAND RUNNER: `env`, `nice` and `timeout` all reach
`gnu.exec_command`, which is `proc_exec_as` under `proc` and `access` under
`fsmode`, and `timeout` reaches `proc_fork` and `proc_waitpid_nohang` on its
own account as well. There is nothing to weaken here — a component has no
process model at all — so the refusal is the whole answer rather than a
missing feature, and the primitives are covered on the wasm side by their own
refusal tests in `internal/e2e` instead.

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
                    sha512sum, b2sum and the ten digests of cksum,
                    which GNU also builds from one source: the option
                    surface, the file-name escaping and the check-line
                    grammar, parameterised by the digest each utility
                    names. cksum widens two rules of that grammar and
                    the module carries both behind one flag — a base64
                    digest is read wherever a hex one is, and a line's
                    TAG chooses the algorithm when no -a did. Two of the
                    ten, `sha2` and `sha3`, name a family rather than a
                    digest: -l picks the member when computing and the
                    line's own tag picks it when checking. The four
                    checksums cksum offers that are NOT digests (the
                    POSIX crc, crc32b, and sum's bsd and sysv) are
                    std/hash
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
  multicall/fern-coreutils.fern
                    all of them in ONE binary, which picks the utility
                    from the basename of argv[0] — used through
                    symlinks named after the utilities, which is what
                    the release archive ships. It lives in its own
                    directory because `coreutils/*.fern` means "a
                    utility" to the corpus, the bench and the macOS
                    lane, and this is not one
internal/coreutils/
  harness_test.go   the oracle harness (this file's "How parity is enforced"),
                    including the per-case working directory and resulting-tree
                    comparison a filesystem-MUTATING utility needs
  longdouble_test.go
                    the long double each target gets, which the
                    host-oracle corpus cannot see (#8513)
  multicall_test.go the multicall binary: the catalogue gate that fails
                    when a utility is missing from it, the dispatcher
                    answering for itself, and the whole corpus run
                    against it and required to agree with the
                    standalone builds
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
working on: printf's huge-precision cases, which make GNU build a two-gigabyte field,
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
6. Add the utility to `coreutils/multicall/fern-coreutils.fern` in all three
   places — the import, the `catalogue()` table and the dispatch arm in
   `main()`. `TestMulticallCatalogue` fails until you have, and names the
   three.
7. Any Fern quirk or bug you hit on the way gets an issue and a fix, never a
   workaround. That is the project's standing order and it is doubly so here,
   where the whole exercise is to find them.

## One binary

`coreutils/multicall/fern-coreutils.fern` is every utility in a single
binary. It reads the basename of argv[0] and becomes that utility, so it
is used through symlinks named after the utilities:

```
$ ln -s fern-coreutils yes
$ ./yes | head -2
y
y
```

Nothing in `lib/gnu.fern` or the utilities changes to support this, and
that is the point: `gnu.prog()` returns argv[0] verbatim, which under a
symlink is already the utility's own name, path included, so a diagnostic
out of the multicall binary is byte-for-byte the one the standalone build
prints. `TestMulticallParity` runs the whole corpus through it and
requires exactly that.

A utility named as an ARGUMENT — `fern-coreutils yes` — is refused rather
than run. It would see this binary's name in its diagnostics, and Fern has
no module-level mutable state, so there is no `set_program_name` to
correct it with. Supporting that form needs a `set_args` primitive in the
runtime (#9694 — the backends already cache `args()` in a slot a store
could replace); until then the symlink is the only invocation, and the
release archive ships the symlinks so untarring it is the whole install.

Under its own name the binary answers for itself: `--list` prints the
catalogue, `--help` and `--version` do the usual.

Size, Linux x86-64, all 106 utilities, 2026-09-18:

| built by | wall | size |
|---|---|---|
| `bin/fern` | 5.4 s | 3,614,505 B |
| `bin/fern-selfhost` | 38.1 s | 3,367,952 B |

Against 106 standalone binaries at roughly 120 KB each, one binary is
about a quarter of the bytes. Both are published per platform by
`.github/workflows/release.yml` while the self-host compiler is on its way
to becoming the default.

## Performance

### Both compilers, 2026-09-17 — the self-host build does not meet requirement 2

The first run of the two-compiler bench (`fern` = `bin/fern`'s build,
`fern-sh` = `bin/fern-selfhost`'s build of the same source, both `-O`).
153 rows over 32 utilities spanning every cost class, Linux x86-64 on a
4-core container, GNU coreutils 9.4, uutils 0.11.0, two batches each run
with nothing else on the machine.

| | native build | self-host build |
|---|---|---|
| rows faster than GNU | 81 / 152 | **51 / 152** |
| rows faster than uutils | 64 / 152 | **40 / 152** |

**Thirty rows beat GNU as native builds them and lose to GNU as the
self-host builds them**, and twenty-four do the same against uutils. The
median row is 0.53x — the self-host build is about twice as slow — and only
23 of 152 rows are within 5% of native. Those 23 are the startup-bound
utilities (`true`, `false`, `pwd`, `whoami`, `hostid`, `nproc`, the
small-input rows of the digests), where nothing runs for long enough for
codegen to matter and both builds beat GNU by 5x on process startup alone.

So the answer to requirement 2 today is: **Fern is faster than GNU and uutils
when the native compiler builds it, and is not when the self-hosted compiler
does.** That is a release blocker for making the self-host the default, not a
footnote, and it was invisible before the leg existed.

The losses are not spread evenly — they are concentrated in accumulation:

| | native | self-host | self-host / native |
|---|---|---|---|
| `tsort` 100k-edge DAG | 63.52 ms | 47,707.77 ms | **751x slower** |
| `tac` 62 MiB from a pipe | 497.51 ms | did not finish in 60 s | at least 120x |
| `shuf -r -n 1000000` | 561.88 ms | 5,621.81 ms | 10.0x |
| `seq 1 1000000` | 4.82 ms | 36.51 ms | 7.6x |
| `shuf` 62 MiB from a pipe | 3,130.72 ms | 16,241.92 ms | 5.2x |
| `cat` a 62 MiB file | 9.44 ms | 38.07 ms | 4.0x |
| `md5sum` of a 62 MiB file | 371.11 ms | 1,241.00 ms | 3.3x |

`tsort` and `tac` are the shape of the problem rather than two unlucky
utilities. Measured on DAGs of 12.5k to 100k edges, `tsort` is LINEAR under
native (0.014, 0.016, 0.032, 0.064 s) and QUADRATIC under the self-host
(0.661, 3.806, 15.900, 47.646 s), so the ratio grows with the input and the
745x above is a property of that size and not a constant. `tac` from a pipe
has to buffer the whole input, which is the same accumulation, and it is the
one row in the corpus the self-host build cannot finish at all.

#9526 has the mechanism for one such shape — a local bound from a plain
parameter and appended to in a loop, which the self-host brackets so the
accumulator is copied per element rather than per call, 267x on a reproducer
and fixed completely by marking the parameter `own`. `tsort`'s site is not
that one, so the family is wider than the single case identified so far.
Everything else on the list is a constant factor of 2x to 4x, which is
ordinary self-host codegen quality rather than a complexity bug.

Full run, every row and both flip lists:
`docs/COREUTILS-BOTH-COMPILERS-2026-09-17.md`.

Two things this measurement does NOT say. It does not say the self-host
miscompiles anything: `TestSelfHostCoreutilsParity` holds both builds to the
same corpus and all 104 utilities compile and agree. And it does not say the
native numbers moved — they match the 2026-09-13 survey below where the rows
overlap.

**Whole catalogue, 2026-09-13**, Linux x86-64 (a 4-core container; GNU
coreutils 9.4; uutils 0.0.24 as the Debian multi-call binary). Every
utility's workloads file ran once through `scripts/coreutils-bench` with
other work on the same cores — 438 rows over 81 utilities — to rank the
losses, and the utilities changed in #9176 and #9188 were re-run alone
afterwards; those rows are the table below. `test` and `uptime` have no
GNU binary at `/usr/bin` on this host and are unmeasured.

Where Fern wins outright (every row at or above 1x): `basename`,
`csplit`, `df`, `dirname`, `echo`, `false`, `fold`, `hostid`, `id`,
`mkdir`, `mknod`, `mktemp`, `nproc`, `pwd`, `readlink`, `rm`, `rmdir`,
`runcon`, `sleep`, `tee`, `true`, `tsort`, `uname`, `users`, `wc`,
`whoami`, `yes`; `seq` on every row; `od` on every row but `-t f8`; `uniq`
on every row but `-f1 -c`. The
startup-bound utilities the loaded survey put under 1x — `arch`, `groups`,
`tty`, `link`, `unlink`, `ln`, `mv`, `mkfifo`, `truncate`, `realpath`, the
`chmod`/`chown`/`chgrp`/`chcon` tree rows within 10% — retire a fortieth of
GNU's instructions under callgrind (`arch` 6,132 against 282,938) with
fewer syscalls, so those rows are load noise on sub-millisecond runs and
not losses.

Where Fern loses, by cause:

- **The backend's byte loop and call floor** — an indexed byte loop is ~40
  retired instructions a byte and a helper call ~60, against C's 1–3:
  the digests (`b2sum`, `md5sum`, `sha*sum`, `cksum` 0.08–0.5x), `base32` /
  `base64` / `basenc` (0.1–0.5x), `sum` (0.3–0.6x), `tr` (0.14–0.24x),
  `cat -A` (0.23x), `tac` (0.2–0.6x), `nl` (0.2–0.4x), `sort` (0.06–0.5x:
  the comparison and, under `-n`, the digit walk), `fmt` (0.22x: the
  break chooser's inner turn), `factor`'s semiprimes, `numfmt`'s remaining
  2.5x, `expr`'s class-anchored regexp, `shuf`'s generator, `od -t f8`
  (0.04x: the long-double library's per-value formatting), and what is
  left of `join` (0.34x) and `cat -n` (0.49x). Each has had its per-line
  copies removed; the rest is codegen.
- **The fd-relative filesystem primitive (#9074)** — `du` (0.5–0.9x), whose
  full-path `stat` costs 9 µs a call against 4.
- **A format string re-parsed per record (#9281)** — `ls -l` and `vdir`
  (0.90x), where 8.7% of a long listing goes to reading `%b %e %H:%M`
  four thousand times. See the ls subsection below, which also carries
  what the rest of that profile is.
- **Within noise or a single row** — `head -n 10` from a pipe, `tail -n
  4000000`, `split -n r/8`, `cut -s`, `comm -12` (0.77x), `uniq -f1 -c`
  (0.61x: the field walk and the key copy on the non-plain path), `env`
  with 60 assignments, `printf` cycling floats, `dircolors` on a 200k-entry
  config (0.70x).

The utilities this branch changed, re-run alone (mean ± σ, ≥20 runs;
ratios above 1 mean Fern is faster):

| utility | workload | fern (ms) | gnu (ms) | uutils (ms) | gnu / fern | uutils / fern |
|---|---|---|---|---|---|---|
| `join` | join two 1M-line files | 397.41 ± 37.88 | 136.91 ± 10.26 | 210.20 ± 16.59 | 0.34× | 0.53× |
| `join` | join with half unpairable | 335.84 ± 23.82 | 105.67 ± 12.75 | 171.60 ± 16.92 | 0.31× | 0.51× |
| `join` | join -a1 -a2 two 1M-line files | 398.26 ± 41.65 | 137.91 ± 12.72 | 185.18 ± 18.97 | 0.35× | 0.46× |
| `join` | join -o 0,1.2,2.3 two 1M-line files | 355.95 ± 33.53 | 121.36 ± 6.83 | 197.28 ± 14.92 | 0.34× | 0.55× |
| `join` | join -v1 two 1M-line files | 297.17 ± 27.11 | 101.77 ± 9.00 | 171.51 ± 12.74 | 0.34× | 0.58× |
| `dircolors` | dircolors | 0.42 ± 0.20 | 1.15 ± 0.25 | 2.07 ± 0.49 | 2.71× | 4.88× |
| `dircolors` | dircolors -p | 0.68 ± 0.18 | 1.03 ± 0.20 | 1.92 ± 0.27 | 1.51× | 2.82× |
| `dircolors` | dircolors --print-ls-colors | 0.20 ± 0.17 | 0.94 ± 0.27 | 1.78 ± 0.25 | 4.69× | 8.88× |
| `dircolors` | dircolors a 200k-entry config | 44.05 ± 3.54 | 30.64 ± 2.75 | 76.76 ± 6.45 | 0.70× | 1.74× |
| `dircolors` | dircolors --print-ls-colors a 200k-entry config | 35.76 ± 3.45 | 42.93 ± 4.21 | 77.86 ± 7.30 | 1.20× | 2.18× |
| `fmt` | fmt (default) of a 40 MiB file | 1545.29 ± 130.25 | 346.33 ± 28.27 | 347.86 ± 40.59 | 0.22× | 0.23× |
| `fmt` | fmt -w 40 of a 40 MiB file | 1411.18 ± 115.52 | 333.05 ± 30.26 | 410.99 ± 38.79 | 0.24× | 0.29× |
| `fmt` | fmt -s -w 40 of a 40 MiB file | 1441.18 ± 119.43 | 324.57 ± 28.94 | 409.41 ± 33.43 | 0.23× | 0.28× |
| `fmt` | fmt -u -w 40 of a 40 MiB file | 1417.59 ± 89.06 | 335.06 ± 24.88 | 394.13 ± 39.16 | 0.24× | 0.28× |
| `fmt` | fmt (default) of a 38 MiB wrapped file | 1652.32 ± 126.15 | 398.69 ± 44.32 | 451.48 ± 42.33 | 0.24× | 0.27× |
| `fmt` | fmt -c -w 60 of a 38 MiB wrapped file | 1656.90 ± 126.76 | 376.52 ± 39.26 | 455.49 ± 47.37 | 0.23× | 0.27× |
| `fmt` | fmt -p "" -w 40 of a 38 MiB wrapped file | 1441.17 ± 140.03 | 338.21 ± 36.86 | 504.93 ± 52.79 | 0.23× | 0.35× |
| `fmt` | fmt (default) from a pipe | 1507.29 ± 138.82 | 462.22 ± 35.61 | 405.87 ± 35.66 | 0.31× | 0.27× |
| `comm` | comm over 2M + 1M sorted lines | 134.70 ± 13.16 | 124.04 ± 6.94 | 372.69 ± 32.99 | 0.92× | 2.77× |
| `comm` | comm -12 over 2M + 1M sorted lines | 129.05 ± 9.26 | 98.97 ± 8.25 | 220.63 ± 21.94 | 0.77× | 1.71× |
| `comm` | comm --total over 2M + 1M sorted lines | 134.65 ± 10.66 | 125.81 ± 10.59 | 366.40 ± 27.06 | 0.93× | 2.72× |
| `comm` | comm of a 2M-line file with itself | 116.77 ± 16.05 | 141.19 ± 12.88 | 401.61 ± 29.65 | 1.21× | 3.44× |
| `comm` | comm -123 of a 2M-line file with itself | 102.54 ± 9.51 | 111.04 ± 12.30 | 83.96 ± 5.53 | 1.08× | 0.82× |
| `uniq` | uniq over 4M lines in groups of 4 | 94.72 ± 8.07 | 103.17 ± 7.43 | 396.04 ± 26.53 | 1.09× | 4.18× |
| `uniq` | uniq -c over 4M lines in groups of 4 | 111.44 ± 11.63 | 138.20 ± 9.65 | 509.87 ± 36.26 | 1.24× | 4.58× |
| `uniq` | uniq -d over 4M lines in groups of 4 | 98.72 ± 7.35 | 100.26 ± 9.43 | 408.85 ± 29.34 | 1.02× | 4.14× |
| `uniq` | uniq over 4M distinct lines | 84.05 ± 8.70 | 96.98 ± 10.31 | 757.30 ± 53.77 | 1.15× | 9.01× |
| `uniq` | uniq -u over 4M distinct lines | 88.17 ± 10.09 | 104.93 ± 8.99 | 764.49 ± 58.78 | 1.19× | 8.67× |
| `uniq` | uniq over 2M 44-byte distinct lines | 82.02 ± 7.79 | 162.35 ± 11.83 | 512.42 ± 47.69 | 1.98× | 6.25× |
| `uniq` | uniq -f1 -c over 4M lines | 242.23 ± 18.91 | 148.18 ± 17.49 | 517.11 ± 50.48 | 0.61× | 2.13× |
| `uniq` | uniq from a pipe | 118.00 ± 28.58 | 189.33 ± 17.06 | 450.21 ± 40.84 | 1.60× | 3.82× |
| `seq` | seq 1 1000000 | 3.51 ± 0.64 | 6.67 ± 0.71 | 336.48 ± 34.63 | 1.90× | 95.90× |
| `seq` | seq -s, 1 1000000 | 3.50 ± 0.39 | 6.92 ± 0.75 | 210.05 ± 21.86 | 1.98× | 60.00× |
| `seq` | seq -w 1 1000000 | 3.41 ± 0.39 | 176.59 ± 14.92 | 331.21 ± 32.32 | 51.77× | 97.10× |
| `seq` | seq 0 0.001 1000 | 3.41 ± 0.44 | 160.17 ± 23.75 | 385.06 ± 45.50 | 46.95× | 112.87× |
| `seq` | seq -f %.3f 0 0.001 1000 | 3.42 ± 0.42 | 156.36 ± 18.44 | 399.18 ± 29.50 | 45.74× | 116.77× |
| `seq` | seq 1 2 1000000 | 1.99 ± 0.25 | 4.26 ± 0.47 | 174.93 ± 14.47 | 2.15× | 88.03× |
| `seq` | seq 1000000 -1 1 | 34.97 ± 3.46 | 171.54 ± 17.52 | 343.40 ± 30.99 | 4.90× | 9.82× |
| `cat` | cat a 62 MiB file | 8.86 ± 0.83 | 8.08 ± 0.68 | 3.87 ± 0.46 | 0.91× | 0.44× |
| `cat` | cat two 62 MiB files | 16.61 ± 1.52 | 14.73 ± 1.33 | 5.96 ± 0.72 | 0.89× | 0.36× |
| `cat` | cat from a pipe | 72.69 ± 59.73 | 54.20 ± 28.93 | 46.82 ± 22.23 | 0.75× | 0.64× |
| `cat` | cat -n a 62 MiB file | 273.44 ± 28.84 | 133.50 ± 10.93 | 1284.66 ± 114.08 | 0.49× | 4.70× |
| `cat` | cat -s a 62 MiB file | 76.33 ± 4.95 | 62.57 ± 5.19 | 1046.02 ± 86.18 | 0.82× | 13.70× |
| `cat` | cat -A a 62 MiB file | 314.90 ± 21.88 | 71.42 ± 11.01 | 1589.55 ± 111.92 | 0.23× | 5.05× |
| `od` | od default of a 62 MiB file | 911.84 ± 90.13 | 3043.46 ± 255.42 | 3216.49 ± 240.71 | 3.34× | 3.53× |
| `od` | od -t x1 of a 62 MiB file | 738.01 ± 68.89 | 4893.52 ± 291.30 | 4818.93 ± 377.70 | 6.63× | 6.53× |
| `od` | od -t x1 -w64 of a 62 MiB file | 537.57 ± 58.94 | 5063.93 ± 393.13 | 3798.88 ± 266.05 | 9.42× | 7.07× |
| `od` | od -t x8 of a 62 MiB file | 638.72 ± 73.25 | 848.13 ± 77.89 | 1578.92 ± 151.42 | 1.33× | 2.47× |
| `od` | od -c of a 62 MiB file | 960.55 ± 94.26 | 5821.26 ± 304.58 | 4661.49 ± 371.57 | 6.06× | 4.85× |
| `od` | od -A n -t x1 of a 62 MiB file | 639.94 ± 39.63 | 5019.29 ± 321.12 | 4898.72 ± 298.22 | 7.84× | 7.65× |
| `od` | od -S 4 of a 62 MiB file | 201.90 ± 20.51 | 476.61 ± 51.47 | 3181.82 ± 269.38 | 2.36× | 15.76× |
| `od` | od -t f8 of a 1 MiB file | 3133.23 ± 278.43 | 136.62 ± 13.11 | 55.41 ± 6.33 | 0.04× | 0.02× |
| `du` | 4000 files in one directory | 7.36 ± 0.85 | 4.68 ± 0.49 | 8.95 ± 1.04 | 0.64× | 1.22× |
| `du` | -a over 4000 files | 8.19 ± 0.87 | 5.55 ± 0.66 | 24.23 ± 5.78 | 0.68× | 2.96× |
| `du` | a 60-deep tree | 4.07 ± 0.53 | 2.49 ± 0.31 | 25.91 ± 6.36 | 0.61× | 6.37× |
| `du` | -a over a 60-deep tree | 4.36 ± 0.47 | 2.72 ± 0.32 | 34.48 ± 6.69 | 0.62× | 7.90× |
| `du` | --apparent-size of the whole tree | 11.37 ± 1.39 | 5.89 ± 0.64 | 29.30 ± 5.38 | 0.52× | 2.58× |
| `du` | -h -c of two trees | 10.56 ± 1.36 | 6.12 ± 0.67 | 30.27 ± 5.54 | 0.58× | 2.86× |
| `du` | --inodes of the whole tree | 10.41 ± 1.13 | 5.93 ± 0.58 | 30.84 ± 5.61 | 0.57× | 2.96× |
| `du` | -l (no seen set) over 4000 files | 5.46 ± 1.43 | 4.93 ± 0.73 | 9.00 ± 0.93 | 0.90× | 1.65× |
| `du` | du of one small directory | 4.00 ± 0.39 | 2.45 ± 0.38 | 8.18 ± 0.98 | 0.61× | 2.04× |
| `sort` | sort 500k lines | 290.97 ± 30.07 | 85.51 ± 4.22 | 80.06 ± 5.17 | 0.29× | 0.28× |
| `sort` | sort -n 500k numbers | 867.89 ± 79.16 | 124.04 ± 5.10 | 176.78 ± 7.93 | 0.14× | 0.20× |
| `sort` | sort -k2,2n 500k lines | 1973.43 ± 168.38 | 123.40 ± 5.11 | 176.67 ± 11.72 | 0.06× | 0.09× |
| `sort` | sort -k1,1 500k lines | 774.39 ± 62.41 | 98.45 ± 4.66 | 124.61 ± 7.79 | 0.13× | 0.16× |
| `sort` | sort -u 500k lines | 309.36 ± 33.95 | 108.49 ± 6.80 | 98.09 ± 5.38 | 0.35× | 0.32× |
| `sort` | sort -r 500k lines | 285.06 ± 25.97 | 86.14 ± 2.35 | 76.98 ± 4.35 | 0.30× | 0.27× |
| `sort` | sort -s 500k lines | 302.86 ± 29.11 | 88.63 ± 3.52 | 86.04 ± 4.89 | 0.29× | 0.28× |
| `sort` | sort 500k lines from a pipe | 309.56 ± 27.98 | 153.65 ± 15.98 | 92.62 ± 13.98 | 0.50× | 0.30× |
| `sort` | sort a sorted 500k-line file | 135.28 ± 13.77 | 34.42 ± 2.87 | 30.34 ± 3.08 | 0.25× | 0.22× |
| `sort` | sort -c a sorted 500k-line file | 21.07 ± 2.48 | 7.73 ± 1.03 | 14.75 ± 1.43 | 0.37× | 0.70× |
| `sort` | sort -m two sorted files | 102.46 ± 8.16 | 30.01 ± 1.84 | 95.87 ± 9.29 | 0.29× | 0.94× |

**sync, touch and date, 2026-09-14**, the same host, each utility's
workloads file run alone (mean ± σ, ≥20 runs; ratios above 1 mean Fern
is faster). Every `sync` and `touch` row and every single-invocation
`date` row wins; the one loss is `date -f` over a file, where the
grammar runs once a line. Its per-line cost is 54k retired instructions
against GNU's roughly 30k, and it is the backend's indexed-byte floor
across the lexer plus the parse state's struct copies, after the two
larger costs were taken out: a tuple carrying a struct is boxed on every
return (a `(boolean, Pc, i32)` from each grammar item cost 330
instructions where returning the struct costs 55), and the word tables
were a strcmp per entry.

| utility | workload | fern (ms) | gnu (ms) | uutils (ms) | gnu / fern | uutils / fern |
|---|---|---|---|---|---|---|
| `sync` | sync | 0.37 ± 0.32 | 1.12 ± 0.21 | 2.01 ± 0.27 | 2.99× | 5.37× |
| `sync` | sync one file | 0.45 ± 0.22 | 1.30 ± 0.23 | 2.13 ± 0.27 | 2.85× | 4.69× |
| `sync` | sync -d one file | 0.42 ± 0.21 | 1.21 ± 0.29 | 2.09 ± 0.30 | 2.91× | 5.04× |
| `sync` | sync -f one file | 0.71 ± 1.13 | 1.45 ± 0.26 | 2.40 ± 0.32 | 2.06× | 3.40× |
| `sync` | sync 200 files in one run | 8.60 ± 1.19 | 9.44 ± 0.94 | 2.63 ± 0.32 | 1.10× | 0.31× |
| `sync` | sync -f 200 files in one run | 18.05 ± 1.43 | 20.97 ± 4.31 | 20.88 ± 1.91 | 1.16× | 1.16× |
| `sync` | sync 200 missing names | 0.90 ± 0.64 | 1.59 ± 0.26 | 2.12 ± 0.25 | 1.76× | 2.35× |
| `touch` | touch one file | 0.46 ± 0.20 | 1.23 ± 0.24 | 2.18 ± 0.27 | 2.70× | 4.77× |
| `touch` | touch 200 files in one run | 1.19 ± 0.23 | 1.69 ± 0.30 | 2.67 ± 0.35 | 1.42× | 2.24× |
| `touch` | touch -d relative 200 files | 1.20 ± 0.22 | 1.69 ± 0.27 | 2.95 ± 0.37 | 1.41× | 2.45× |
| `touch` | touch -t 200 files | 1.09 ± 0.19 | 1.59 ± 0.88 | 2.59 ± 0.40 | 1.46× | 2.38× |
| `touch` | touch -r -d 200 files | 1.11 ± 0.23 | 1.58 ± 0.24 | 3.57 ± 0.41 | 1.42× | 3.23× |
| `touch` | touch -a -m -h 200 files | 0.81 ± 0.17 | 1.49 ± 0.23 | 2.66 ± 0.38 | 1.84× | 3.28× |
| `touch` | touch -c 200 missing names | 0.64 ± 0.18 | 1.27 ± 0.22 | 2.33 ± 0.33 | 1.98× | 3.65× |
| `touch` | touch create and remove 10 files | 1.81 ± 0.27 | 2.46 ± 0.29 | 3.40 ± 0.41 | 1.36× | 1.88× |
| `date` | date | 0.29 ± 0.07 | 1.32 ± 0.14 | 2.18 ± 0.19 | 4.56× | 7.55× |
| `date` | date -d fixed | 0.30 ± 0.08 | 1.32 ± 0.13 | 2.21 ± 0.15 | 4.43× | 7.41× |
| `date` | date -d relative | 0.41 ± 0.44 | 1.40 ± 0.24 | 3.02 ± 0.19 | 3.40× | 7.31× |
| `date` | date every conversion | 0.31 ± 0.10 | 1.34 ± 0.20 | 2.23 ± 0.17 | 4.29× | 7.13× |
| `date` | date -f 10000 lines | 83.31 ± 2.09 | 40.54 ± 2.19 | 13.61 ± 1.04 | 0.49× | 0.16× |
| `date` | date -u -R | 0.35 ± 0.09 | 1.34 ± 0.15 | 2.20 ± 0.20 | 3.88× | 6.35× |
| `date` | date --debug | 0.35 ± 0.09 | 1.34 ± 0.12 | 2.89 ± 0.21 | 3.87× | 8.33× |

**dd, 2026-09-14**, the same host, its workloads file run alone (mean ± σ
over ≥20 runs; a ratio above 1 means Fern is faster):

| utility | workload | fern (ms) | gnu (ms) | uutils (ms) | gnu / fern | uutils / fern |
|---|---|---|---|---|---|---|
| `dd` | dd one small copy | 0.47 ± 0.18 | 1.24 ± 0.24 | 2.39 ± 0.69 | 2.65× | 5.10× |
| `dd` | dd 8MiB bs=512 | 17.63 ± 1.80 | 14.97 ± 1.92 | 17.26 ± 1.90 | 0.85× | 0.98× |
| `dd` | dd 8MiB bs=4096 | 8.16 ± 0.76 | 7.94 ± 0.83 | 8.19 ± 0.97 | 0.97× | 1.00× |
| `dd` | dd 8MiB bs=64k | 3.63 ± 0.40 | 3.96 ± 0.48 | 5.23 ± 0.55 | 1.09× | 1.44× |
| `dd` | dd 8MiB bs=1M | 4.10 ± 0.42 | 4.09 ± 0.49 | 5.69 ± 0.64 | 1.00× | 1.39× |
| `dd` | dd 8MiB default block | 17.23 ± 1.48 | 14.11 ± 1.24 | 16.82 ± 1.47 | 0.82× | 0.98× |
| `dd` | dd 8MiB ibs 4k obs 64k | 4.63 ± 0.62 | 4.48 ± 0.83 | 6.12 ± 0.66 | 0.97× | 1.32× |
| `dd` | dd 8MiB conv=swab | 22.25 ± 2.05 | 4.96 ± 0.51 | 7.86 ± 0.82 | 0.22× | 0.35× |
| `dd` | dd 8MiB conv=ucase | 25.72 ± 3.14 | 6.51 ± 1.14 | 7.52 ± 0.72 | 0.25× | 0.29× |
| `dd` | dd 8MiB conv=sync | 3.81 ± 0.40 | 3.69 ± 0.45 | 5.36 ± 0.59 | 0.97× | 1.41× |
| `dd` | dd conv=block cbs=16 | 38.30 ± 4.57 | 15.68 ± 1.52 | 28.52 ± 3.47 | 0.41× | 0.74× |
| `dd` | dd conv=unblock cbs=16 | 58.97 ± 5.97 | 12.00 ± 1.90 | 8.09 ± 0.85 | 0.20× | 0.14× |
| `dd` | dd 8MiB skip and seek | 4.12 ± 0.46 | 4.49 ± 0.54 | 6.05 ± 0.63 | 1.09× | 1.47× |
| `dd` | dd 64MiB zero to null bs=512 | 52.46 ± 5.98 | 32.68 ± 3.70 | 42.24 ± 4.17 | 0.62× | 0.81× |
| `dd` | dd 64MiB zero to null bs=64k | 2.12 ± 0.27 | 2.61 ± 0.33 | 4.03 ± 0.45 | 1.23× | 1.90× |
| `dd` | dd 8MiB from a pipe | 8.26 ± 6.74 | 7.24 ± 5.38 | 7.57 ± 1.91 | 0.88× | 0.92× |
| `dd` | dd 8MiB from a pipe fullblock | 8.18 ± 6.84 | 6.73 ± 2.64 | 7.10 ± 0.66 | 0.82× | 0.87× |

**The `bs=64k` copy row was 0.09× a day earlier, and the fix was three
reference-counting bugs rather than anything in dd.** Worth recording
because the SHAPE of that measurement is what found them: 40.2 ms against
GNU's 3.6 with 37.6 ms of it in SYSTEM time, while a bare Fern read/write
loop over the same bytes beat GNU outright. The loop was never the problem —
the read buffer was never reclaimed, so every record faulted sixteen fresh
pages in instead of reusing the block (`ru_minflt` 17,531 against 158 now).
#9244 (a local aliasing a borrowed parameter), #9245 (the per-call Result
box, once the failure arm binds its payload) and #9246 (`x = f(x)` where the
callee can hand its argument back) each have a reproducer of its own in
`docs/rc-log/`.

What is still behind is the byte-rewriting conversions — `conv=swab`,
`conv=ucase`, `conv=block` and `conv=unblock` — and the small-`bs` rows.
The conversions build their output through `__alloc_u8` plus a `.with` per
byte, which is the same shape `tr` uses and a different optimisation from
this one; `bs=512` is 128× the syscalls of `bs=64k` for the same bytes, so
the gap there is per-call overhead rather than throughput. Neither is a
correctness question and neither is dd-specific.

**nice, 2026-09-14**, the same host, its workloads file run alone (mean ±
σ, ≥20 runs; ratios above 1 mean Fern is faster).

| utility | workload | fern (ms) | gnu (ms) | uutils (ms) | gnu / fern | uutils / fern |
|---|---|---|---|---|---|---|
| `nice` | nice reads the niceness | 0.21 ± 0.20 | 1.13 ± 0.65 | 1.86 ± 0.56 | 5.47× | 9.00× |
| `nice` | nice runs a command | 1.31 ± 0.54 | 1.76 ± 0.57 | 2.78 ± 0.58 | 1.35× | 2.12× |
| `nice` | nice -n 5 a command | 1.11 ± 1.00 | 1.71 ± 0.52 | 2.62 ± 0.72 | 1.54× | 2.37× |
| `nice` | nice -5 a command | 1.22 ± 0.55 | 1.72 ± 0.44 | 2.55 ± 0.39 | 1.41× | 2.09× |
| `nice` | nice --adjustment=5 a command | 1.17 ± 0.45 | 2.03 ± 0.80 | 2.67 ± 0.62 | 1.74× | 2.29× |
| `nice` | nice -n -5 a command | 1.13 ± 0.60 | 1.84 ± 0.54 | 2.76 ± 0.62 | 1.63× | 2.43× |
| `nice` | nice a command not found | 0.31 ± 0.38 | 1.18 ± 0.74 | 2.15 ± 1.89 | 3.79× | 6.88× |
| `nice` | nice an invalid adjustment | 0.24 ± 0.23 | 1.02 ± 0.49 | 1.78 ± 0.55 | 4.18× | 7.27× |

Every row wins, and the shape says why: `nice` is startup plus one syscall
in the rows that do not exec, and startup plus `setpriority` and `execve` in
the ones that do. So the two rows with no exec at all — the niceness read
and the invalid-adjustment refusal — are the whole of the startup margin a
static binary with no dynamic loader gets, 5.5× and 4.2×, while the rows
that exec pay the same `execve` on all three sides and land at 1.3–1.7×.
The argument handling does not appear: `-5`, `-n 5` and
`--adjustment=5` are within a σ of each other, so the obsolescent form's
pre-pass and getopt_long's prefix match both cost nothing next to the exec.

**shred, 2026-09-14**, the same host, its workloads file run alone (mean ±
σ, ≥20 runs; ratios above 1 mean Fern is faster).

| utility | workload | fern (ms) | gnu (ms) | uutils (ms) | gnu / fern | uutils / fern |
|---|---|---|---|---|---|---|
| `shred` | shred one small file | 2.57 ± 0.97 | 3.62 ± 0.80 | 5.31 ± 1.04 | 1.41× | 2.07× |
| `shred` | shred 200 small files | 490.77 ± 37.87 | 712.54 ± 26.95 | 1078.32 ± 38.24 | 1.45× | 2.20× |
| `shred` | shred 200 small files in one run | 457.97 ± 42.49 | 451.79 ± 46.35 | 575.05 ± 50.54 | 0.99× | 1.26× |
| `shred` | shred 4MiB default passes | 32.65 ± 4.73 | 42.18 ± 8.12 | 23.24 ± 2.19 | 1.29× | 0.71× |
| `shred` | shred 4MiB -n 1 | 13.04 ± 1.75 | 16.02 ± 2.77 | 11.52 ± 1.47 | 1.23× | 0.88× |
| `shred` | shred 4MiB -n 1 -z | 18.86 ± 2.91 | 24.25 ± 2.60 | 16.30 ± 1.45 | 1.29× | 0.86× |
| `shred` | shred 4MiB -n 1 from a file source | 7.58 ± 1.10 | 16.78 ± 6.60 | 5.97 ± 1.76 | 2.21× | 0.79× |
| `shred` | shred 4MiB -n 4 from a file source | 23.82 ± 2.53 | 42.83 ± 6.35 | 5.21 ± 0.79 | 1.80× | 0.22× |
| `shred` | shred 4MiB -n 1 -x | 12.57 ± 1.62 | 15.69 ± 2.32 | 12.03 ± 1.65 | 1.25× | 0.96× |
| `shred` | shred 200 sub-block files | 511.36 ± 58.51 | 507.14 ± 71.61 | 494.73 ± 25.57 | 0.99× | 0.97× |
| `shred` | shred -u 200 small files | 492.70 ± 65.62 | 696.74 ± 39.89 | 846.74 ± 88.56 | 1.41× | 1.72× |
| `shred` | shred -n 0 -u 200 small files | 349.06 ± 17.16 | 578.00 ± 74.66 | 567.56 ± 23.96 | 1.66× | 1.63× |
| `shred` | shred -s 4096 of a 4MiB file | 7.70 ± 2.20 | 8.38 ± 1.67 | 9.07 ± 1.20 | 1.09× | 1.18× |

**nohup, 2026-09-15**, the same host, its workloads file run alone (mean ±
σ, ≥20 runs; ratios above 1 mean Fern is faster).

| utility | workload | fern (ms) | gnu (ms) | uutils (ms) | gnu / fern | uutils / fern |
|---|---|---|---|---|---|---|
| `nohup` | nohup a command | 1.27 ± 0.21 | 2.07 ± 0.78 | 3.02 ± 0.39 | 1.63× | 2.37× |
| `nohup` | nohup a command with arguments | 1.22 ± 0.24 | 2.09 ± 0.67 | 3.13 ± 2.18 | 1.70× | 2.55× |
| `nohup` | nohup a command not found | 0.35 ± 0.18 | 1.17 ± 0.27 | 2.05 ± 0.21 | 3.31× | 5.78× |
| `nohup` | nohup no operand | 0.38 ± 0.16 | 1.28 ± 0.31 | 2.22 ± 0.28 | 3.42× | 5.90× |
| `nohup` | nohup a status passed through | 1.28 ± 0.25 | 2.03 ± 0.32 | 2.94 ± 0.39 | 1.58× | 2.29× |

**stty, 2026-09-15**, the same host, its workloads file run alone (mean ± σ,
≥20 runs; ratios above 1 mean Fern is faster). No uutils column: it has no
`stty`.

| utility | workload | fern (ms) | gnu (ms) | gnu / fern |
|---|---|---|---|---|
| `stty` | stty print all | 0.32 ± 0.17 | 1.21 ± 0.29 | 3.74× |
| `stty` | stty print stty-readable | 0.33 ± 0.26 | 1.17 ± 0.46 | 3.56× |
| `stty` | stty print the deviations | 0.41 ± 0.15 | 1.22 ± 0.18 | 2.95× |
| `stty` | stty print the size | 0.36 ± 0.57 | 1.17 ± 0.22 | 3.29× |
| `stty` | stty set one flag | 0.41 ± 0.17 | 1.19 ± 0.34 | 2.90× |
| `stty` | stty set six settings | 0.48 ± 0.17 | 1.28 ± 0.47 | 2.69× |
| `stty` | stty set the reference set | 0.40 ± 0.31 | 1.21 ± 0.32 | 3.03× |
| `stty` | stty set a combination | 0.47 ± 0.39 | 1.12 ± 0.23 | 2.38× |
| `stty` | stty restore a saved line | 0.50 ± 0.46 | 1.38 ± 0.55 | 2.77× |
| `stty` | stty reject a bad name | 0.30 ± 0.36 | 1.14 ± 0.42 | 3.79× |

Every row does the utility's real work, which took finding a terminal a
benchmark harness can hand out: `-F /dev/ptmx` opens a fresh pseudo-terminal
MASTER, and a master answers TCGETS, TCSETS and TIOCGWINSZ like any other
terminal. The whole of `stty` is one ioctl, a table walk and one ioctl back,
so the lead is startup — which is also why the spread is wide at these
magnitudes and why the three print forms cost almost the same.

None of the redirections fire in any of nohup's rows, and no benchmark can
make them: every one needs a TERMINAL on the descriptor, and hyperfine hands the
child pipes. What the rows measure is the part a shell script actually pays —
startup, three isatty questions, a signal disposition and the exec — and the
two that never exec (a name off PATH, no operand) are where the startup lead
shows undiluted, at 3.3× and 3.4×.

**timeout, 2026-09-15**, the same host, its workloads file run alone (mean ±
σ, ≥20 runs; ratios above 1 mean Fern is faster).

| utility | workload | fern (ms) | gnu (ms) | uutils (ms) | gnu / fern | uutils / fern |
|---|---|---|---|---|---|---|
| `timeout` | timeout a command | 1.75 ± 1.45 | 2.24 ± 0.22 | 103.85 ± 1.01 | 1.28× | 59.39× |
| `timeout` | timeout zero disables | 1.54 ± 0.21 | 2.38 ± 0.26 | 3.37 ± 0.54 | 1.55× | 2.19× |
| `timeout` | timeout --foreground | 1.72 ± 0.28 | 2.33 ± 0.23 | 103.73 ± 0.34 | 1.36× | 60.41× |
| `timeout` | timeout -s KILL | 1.74 ± 2.12 | 2.18 ± 0.25 | 103.71 ± 0.26 | 1.26× | 59.67× |
| `timeout` | timeout -k with a grace period | 1.72 ± 0.28 | 2.43 ± 0.36 | 103.83 ± 0.32 | 1.41× | 60.39× |
| `timeout` | timeout -v | 1.57 ± 0.26 | 2.22 ± 0.20 | 103.70 ± 0.29 | 1.42× | 66.16× |
| `timeout` | timeout --preserve-status | 1.63 ± 0.48 | 2.59 ± 2.00 | 103.70 ± 0.38 | 1.59× | 63.51× |
| `timeout` | timeout a fractional duration | 1.50 ± 0.29 | 2.26 ± 0.27 | 103.63 ± 0.37 | 1.51× | 69.09× |
| `timeout` | timeout a suffixed duration | 1.61 ± 0.28 | 2.35 ± 0.25 | 103.89 ± 0.60 | 1.46× | 64.61× |
| `timeout` | timeout a command with arguments | 1.61 ± 0.44 | 2.45 ± 1.16 | 104.31 ± 2.42 | 1.52× | 64.85× |
| `timeout` | timeout a command not found | 0.70 ± 0.21 | 1.53 ± 0.23 | 2.47 ± 0.31 | 2.18× | 3.52× |
| `timeout` | timeout an invalid duration | 0.34 ± 0.50 | 1.17 ± 0.23 | 2.14 ± 0.27 | 3.41× | 6.24× |

Every row wins, and the two-row control says what the implementation costs.
Fern forks a TIMER CHILD where GNU arms a SIGALRM, because nothing here can
observe a delivered signal (#9243) — so `timeout 10 /bin/true` pays one extra
fork and one extra reap, and `timeout 0 /bin/true`, which takes no timer on
either side, is the same run without it: 1.75 ms against 1.54 ms. Two tenths
of a millisecond, against a 0.5 ms startup lead that more than covers it.

uutils is 100 ms on every row whose deadline is real, and 3.4 ms on the two
that are not. That is not startup: its wait is a 100 ms polling loop, so a
command finishing immediately is still noticed a tick later. Fern and GNU
both block until the child is reaped and answer as soon as it is, which is
why `timeout 0` is the only row where uutils is within a factor of thirty.

Eleven of thirteen rows win and the other two are inside a σ on a workload
whose reseeding forks 200 GNU `head`s per run; per file `shred.fern` makes 9
syscalls against GNU's 41. Three measurements got it there, in order of what
they were worth:

- A PATTERN pass is at the write floor — 5.6 ms for 4 MiB against `dd`'s own
  5.8 with the same `fdatasync` — once the pattern block is laid down ONCE per
  pass with `repeat` instead of a byte at a time per block (34 ms to 5.6), and
  once a random pass stops building a pattern block it throws away.
- A RANDOM pass was the whole of what was left, and the cause was not shred:
  `random_bytes` is one `getrandom(2)` per block and the kernel's generator
  runs at 320 MB/s here — `dd if=/dev/urandom` costs the same 13 ms for
  4 MiB — where GNU seeds ISAAC from /dev/urandom once and produces the rest
  in userspace at about twice that. So the default source is now
  `std/rand`'s seeded generator over `buf_push_u64` (#9221), which fills
  4 MiB in 5.6 ms, and every random row moved from 0.61–0.69× to 1.23–1.29×.
  A generator written in Fern could not have closed it before that primitive:
  assembling the bytes cost 8 ns each through an array append and 5 ns
  through `buf_push_byte`, both dearer than the syscall they would replace.
- The block size is 60 KiB: a multiple of three, so every block starts at the
  same point in a pattern's cycle and one block serves the pass, and of 4096,
  so the writes stay aligned.

Before this branch those rows read `join` 0.10x, `dircolors` 0.29x, `fmt`
0.12x, `comm` 0.26x, `uniq` 0.55–0.86x, `seq 1 2 1000000` 0.88x (the
builder take leak, #9179), `cat -n` 0.22x, `od -t x8` 0.18x.

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

**Fern is measured under both of its compilers.** `fern` is `bin/fern`'s
build of a utility and `fern-sh` is `bin/fern-selfhost`'s build of the same
source, and the table carries `gnu / fern-sh` and `uutils / fern-sh` beside
the native pair plus `fern / fern-sh` for what the two compilers cost against
each other. The self-hosted compiler is becoming the default one (CLAUDE.md),
so requirement 2 is not answered by the native column alone: a utility can be
comfortably faster than GNU as native builds it and lose to GNU as the
self-host builds it, and before this leg existed nothing in the tree would
have said so. `FERN_BENCH_COMPILERS=native` drops back to the old single
column.

The self-host leg needs a bound the native leg never did. A self-host build
can be slower by a factor that turns a benchmark into a hang — #8688 has
`seq` roughly 400x slower and growing to 11 GB — so each Fern build runs one
trial of a workload under `timeout` (60 s, `FERN_BENCH_PROBE_TIMEOUT`) before
hyperfine sees it, and a build that does not finish gets `did not finish` in
its cell rather than being dropped from the table. The bound is on time and
not on memory because the arena is a 16 GiB `MAP_NORESERVE` reservation, so
`ulimit -v` and `ulimit -d` refuse every Fern binary at startup.

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

**cksum did not meet the epic's bar when this table was taken**, and the
three groups of rows failed it for three different reasons. The CRC group is
answered below; the other two stand.

- **The CRC rows are the worst in this document, and the cause is one
  instruction.** GNU's 14 ms over 62 MiB is 0.23 ns a byte, which no
  byte-at-a-time loop reaches: it folds the message with `pclmulqdq`, the
  carry-less multiply, sixteen bytes at a time. uutils' 199 ms is a table,
  so the Fern-to-uutils ratio is the codegen comparison and the ratio
  against GNU is not. `std/hash`'s `Cksum` went to a slicing-by-8 table,
  which closed the uutils half, and then to the `__crc32_cksum` fold
  (#9128), which closes the GNU half: the fold table below measures 0.92×
  against a GNU that is itself on `pclmulqdq`. These rows are the before
  side. #9056.
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

Both encodings exist in all four assemblers and the kernel is written
(#9128) — see the fold table below for what it measures. Both are inside
their baselines and neither needs runtime dispatch — `pclmulqdq`
always was, and the arm64 baseline was raised to ARMv8.2-A with the crypto
extensions to take `pmull.1q`. arm64 has a second option worth measuring
against the fold rather than assuming past: `crc32` is in the same baseline,
and reaching cksum's non-reflected CRC from that reflected instruction is an
`rbit` away.

Ranking the three on this host, which is the honest summary: uutils (folding)
9.8 ms, GNU (generic table) 35.7 ms, Fern (slicing-by-8) 199.7 ms.

### Both compilers, 2026-09-22, Linux x86-64 (GNU coreutils 9.12, uutils 0.0.24)

The two-compiler bench over the fifteen utilities the 2026-09-22 codegen
work touched or measured against — #10012 and #10023's tree (`cdfb15d`),
both compilers `-O`, a 4-core container, twenty runs a cell, with the
`od -t f8` row measured BEFORE the float path below was rewritten. Beside
the 2026-09-17 run the self-host column is no longer broadly 2x: it is
within 20% of native on most rows, and ahead on `sort -u`, `tr 0-9 a-j`
and `tail -n 4000000`. Where it still trails by half — `wc` (0.46x),
`uniq` over long lines (0.55x), `head -n -10` from a pipe (0.29x), `seq`
(0.3–0.5x, startup) — the gap is the self-host's byte loop, #8822.


| utility | workload | fern (ms) | fern-sh (ms) | gnu (ms) | uutils (ms) | gnu / fern | uutils / fern | gnu / fern-sh | uutils / fern-sh | fern / fern-sh |
|---|---|---|---|---|---|---|---|---|---|---|
| `sort` | sort 500k lines | 305.52 ± 14.40 | 303.64 ± 13.92 | 96.45 ± 4.95 | 97.20 ± 6.93 | 0.32× | 0.32× | 0.32× | 0.32× | 1.01× |
| `sort` | sort -n 500k numbers | 960.76 ± 33.39 | 1029.83 ± 37.10 | 146.98 ± 5.82 | 193.45 ± 7.99 | 0.15× | 0.20× | 0.14× | 0.19× | 0.93× |
| `sort` | sort -k2,2n 500k lines | 899.44 ± 39.08 | 1041.01 ± 40.76 | 163.06 ± 15.46 | 213.02 ± 10.49 | 0.18× | 0.24× | 0.16× | 0.20× | 0.86× |
| `sort` | sort -k1,1 500k lines | 559.05 ± 35.38 | 614.94 ± 38.98 | 115.21 ± 11.60 | 147.30 ± 8.25 | 0.21× | 0.26× | 0.19× | 0.24× | 0.91× |
| `sort` | sort -u 500k lines | 322.75 ± 16.02 | 313.33 ± 21.53 | 113.69 ± 9.32 | 116.45 ± 8.65 | 0.35× | 0.36× | 0.36× | 0.37× | 1.03× |
| `sort` | sort -r 500k lines | 305.06 ± 12.36 | 301.14 ± 8.86 | 106.90 ± 7.95 | 101.33 ± 7.46 | 0.35× | 0.33× | 0.35× | 0.34× | 1.01× |
| `sort` | sort -s 500k lines | 308.97 ± 18.61 | 307.18 ± 13.49 | 99.99 ± 4.35 | 101.73 ± 11.54 | 0.32× | 0.33× | 0.33× | 0.33× | 1.01× |
| `sort` | sort 500k lines from a pipe | 306.91 ± 16.03 | 312.77 ± 20.98 | 111.13 ± 5.35 | 99.34 ± 6.35 | 0.36× | 0.32× | 0.36× | 0.32× | 0.98× |
| `sort` | sort a sorted 500k-line file | 162.72 ± 14.59 | 148.39 ± 12.11 | 44.66 ± 3.78 | 41.67 ± 5.36 | 0.27× | 0.26× | 0.30× | 0.28× | 1.10× |
| `sort` | sort -c a sorted 500k-line file | 29.15 ± 2.34 | 25.05 ± 3.28 | 9.53 ± 0.74 | 19.27 ± 2.77 | 0.33× | 0.66× | 0.38× | 0.77× | 1.16× |
| `sort` | sort -m two sorted files | 131.65 ± 14.05 | 144.11 ± 13.68 | 39.63 ± 3.16 | 94.53 ± 7.24 | 0.30× | 0.72× | 0.28× | 0.66× | 0.91× |
| `cat` | cat a 62 MiB file | 7.48 ± 1.21 | 7.75 ± 1.60 | 2.65 ± 1.23 | 3.89 ± 0.61 | 0.35× | 0.52× | 0.34× | 0.50× | 0.97× |
| `cat` | cat two 62 MiB files | 12.11 ± 1.58 | 11.06 ± 1.60 | 3.40 ± 0.61 | 5.11 ± 0.78 | 0.28× | 0.42× | 0.31× | 0.46× | 1.10× |
| `cat` | cat from a pipe | 11.55 ± 1.35 | 10.29 ± 1.19 | 4.17 ± 2.32 | 5.01 ± 1.18 | 0.36× | 0.43× | 0.41× | 0.49× | 1.12× |
| `cat` | cat -n a 62 MiB file | 371.34 ± 23.74 | 497.77 ± 26.52 | 178.45 ± 4.06 | 1730.21 ± 24.15 | 0.48× | 4.66× | 0.36× | 3.48× | 0.75× |
| `cat` | cat -s a 62 MiB file | 87.32 ± 3.66 | 129.52 ± 8.79 | 94.13 ± 7.15 | 1408.00 ± 32.04 | 1.08× | 16.13× | 0.73× | 10.87× | 0.67× |
| `cat` | cat -A a 62 MiB file | 305.10 ± 13.57 | 353.35 ± 27.71 | 102.85 ± 8.00 | 2018.60 ± 32.12 | 0.34× | 6.62× | 0.29× | 5.71× | 0.86× |
| `wc` | wc -l of a 62 MiB file | 10.08 ± 1.61 | 9.36 ± 1.62 | 8.37 ± 0.99 | 16.57 ± 1.76 | 0.83× | 1.64× | 0.89× | 1.77× | 1.08× |
| `wc` | wc -c of a 62 MiB file | 0.30 ± 0.14 | 0.28 ± 0.10 | 1.57 ± 0.31 | 2.27 ± 0.39 | 5.31× | 7.68× | 5.54× | 8.01× | 1.04× |
| `wc` | wc of a 62 MiB file | 190.44 ± 14.77 | 409.76 ± 14.62 | 84.84 ± 4.27 | 146.19 ± 10.09 | 0.45× | 0.77× | 0.21× | 0.36× | 0.46× |
| `wc` | wc -w of a 62 MiB file | 191.77 ± 8.51 | 420.88 ± 20.77 | 87.36 ± 11.36 | 157.84 ± 5.78 | 0.46× | 0.82× | 0.21× | 0.38× | 0.46× |
| `wc` | wc -L of a 62 MiB file | 195.52 ± 17.27 | 411.48 ± 18.81 | 85.64 ± 13.47 | 141.93 ± 7.23 | 0.44× | 0.73× | 0.21× | 0.34× | 0.48× |
| `wc` | wc -l from a pipe | 17.16 ± 3.46 | 16.97 ± 3.75 | 18.49 ± 5.15 | 27.97 ± 3.84 | 1.08× | 1.63× | 1.09× | 1.65× | 1.01× |
| `fmt` | fmt (default) of a 40 MiB file | 1713.74 ± 35.97 | 1907.91 ± 48.56 | 430.30 ± 18.10 | 464.38 ± 19.13 | 0.25× | 0.27× | 0.23× | 0.24× | 0.90× |
| `fmt` | fmt -w 40 of a 40 MiB file | 1537.49 ± 30.40 | 1794.33 ± 50.16 | 418.15 ± 26.32 | 549.10 ± 53.08 | 0.27× | 0.36× | 0.23× | 0.31× | 0.86× |
| `fmt` | fmt -s -w 40 of a 40 MiB file | 1531.34 ± 48.98 | 1784.80 ± 34.91 | 401.93 ± 19.28 | 538.96 ± 24.67 | 0.26× | 0.35× | 0.23× | 0.30× | 0.86× |
| `fmt` | fmt -u -w 40 of a 40 MiB file | 1532.21 ± 48.91 | 1788.48 ± 49.77 | 400.59 ± 18.02 | 500.86 ± 24.39 | 0.26× | 0.33× | 0.22× | 0.28× | 0.86× |
| `fmt` | fmt (default) of a 38 MiB wrapped file | 1820.47 ± 45.80 | 1991.73 ± 52.34 | 484.48 ± 19.36 | 585.36 ± 22.99 | 0.27× | 0.32× | 0.24× | 0.29× | 0.91× |
| `fmt` | fmt -c -w 60 of a 38 MiB wrapped file | 1705.90 ± 45.28 | 1889.26 ± 49.21 | 449.03 ± 16.97 | 615.98 ± 38.22 | 0.26× | 0.36× | 0.24× | 0.33× | 0.90× |
| `fmt` | fmt -p "" -w 40 of a 38 MiB wrapped file | 1450.23 ± 32.91 | 1701.57 ± 37.77 | 411.70 ± 31.35 | 700.64 ± 34.86 | 0.28× | 0.48× | 0.24× | 0.41× | 0.85× |
| `fmt` | fmt (default) from a pipe | 1748.03 ± 68.44 | 1900.22 ± 38.53 | 539.15 ± 12.49 | 524.56 ± 23.06 | 0.31× | 0.30× | 0.28× | 0.28× | 0.92× |
| `fmt` | fmt (default) of one 200k-line paragraph | 287.79 ± 21.25 | 357.69 ± 18.44 | 56.47 ± 6.50 | 162.19 ± 9.18 | 0.20× | 0.56× | 0.16× | 0.45× | 0.80× |
| `ptx` | ptx 120k words | 198.65 ± 6.86 | 242.03 ± 6.28 | 42.21 ± 1.89 | exit 1 | 0.21× | — | 0.17× | — | 0.82× |
| `ptx` | ptx -G 120k words | 512.07 ± 13.92 | 590.27 ± 23.07 | 57.93 ± 4.29 | 175.25 ± 6.03 | 0.11× | 0.34× | 0.10× | 0.30× | 0.87× |
| `ptx` | ptx -O 120k words | 511.35 ± 10.28 | 594.64 ± 13.06 | 52.96 ± 4.67 | exit 1 | 0.10× | — | 0.09× | — | 0.86× |
| `ptx` | ptx -T 120k words | 521.13 ± 18.34 | 590.74 ± 18.77 | 50.78 ± 5.23 | exit 1 | 0.10× | — | 0.09× | — | 0.88× |
| `ptx` | ptx -W a regexp alphabet | 724.59 ± 37.99 | 1684.62 ± 24.28 | 225.96 ± 9.10 | exit 1 | 0.31× | — | 0.13× | — | 0.43× |
| `ptx` | ptx prose with sentences | 87.69 ± 3.32 | 106.26 ± 15.13 | 29.41 ± 2.73 | exit 1 | 0.34× | — | 0.28× | — | 0.83× |
| `ptx` | ptx -A prose | 135.63 ± 14.58 | 149.11 ± 6.49 | 37.90 ± 5.74 | exit 1 | 0.28× | — | 0.25× | — | 0.91× |
| `ptx` | ptx from a pipe | 199.30 ± 4.84 | 251.30 ± 11.89 | 44.14 ± 3.75 | exit 1 | 0.22× | — | 0.18× | — | 0.79× |
| `tr` | tr 0-9 a-j over a 62 MiB file | 192.85 ± 19.56 | 167.46 ± 5.07 | 41.71 ± 6.07 | 941.63 ± 26.59 | 0.22× | 4.88× | 0.25× | 5.62× | 1.15× |
| `tr` | tr -d 0-4 over a 62 MiB file | 173.98 ± 16.07 | 200.86 ± 15.20 | 81.63 ± 3.22 | 430.98 ± 26.47 | 0.47× | 2.48× | 0.41× | 2.15× | 0.87× |
| `tr` | tr -s 0-9 over a 62 MiB file | 199.07 ± 5.82 | 264.76 ± 21.67 | 524.62 ± 24.33 | 766.71 ± 24.58 | 2.64× | 3.85× | 1.98× | 2.90× | 0.75× |
| `tr` | tr -cd digits over a 62 MiB file | 194.94 ± 16.48 | 214.95 ± 15.66 | 53.93 ± 3.80 | 580.24 ± 18.33 | 0.28× | 2.98× | 0.25× | 2.70× | 0.91× |
| `tr` | tr from a pipe | 200.95 ± 19.66 | 188.04 ± 17.56 | 82.08 ± 9.05 | 1009.17 ± 28.01 | 0.41× | 5.02× | 0.44× | 5.37× | 1.07× |
| `cut` | cut -f2 -d, of a 90 MiB table | 106.50 ± 12.31 | 122.42 ± 16.66 | 90.73 ± 3.27 | 86.37 ± 4.06 | 0.85× | 0.81× | 0.74× | 0.71× | 0.87× |
| `cut` | cut -f1,3-5 -d, of a 90 MiB table | 130.51 ± 5.48 | 214.74 ± 10.35 | 198.08 ± 8.56 | 146.95 ± 14.63 | 1.52× | 1.13× | 0.92× | 0.68× | 0.61× |
| `cut` | cut --complement -f2 -d, of a 90 MiB table | 132.90 ± 6.29 | 212.11 ± 10.83 | 193.71 ± 6.14 | 140.31 ± 6.34 | 1.46× | 1.06× | 0.91× | 0.66× | 0.63× |
| `cut` | cut -s -f4 -d, of a 90 MiB table | 114.24 ± 6.14 | 145.60 ± 8.39 | 121.41 ± 7.84 | 111.51 ± 6.55 | 1.06× | 0.98× | 0.83× | 0.77× | 0.78× |
| `cut` | cut -c1-10 of a 90 MiB table | 57.43 ± 4.03 | 74.35 ± 4.25 | 51.90 ± 4.60 | 58.50 ± 4.02 | 0.90× | 1.02× | 0.70× | 0.79× | 0.77× |
| `cut` | cut -f2 -d, from a pipe | 116.03 ± 6.73 | 133.87 ± 9.96 | 98.52 ± 7.42 | 189.60 ± 11.51 | 0.85× | 1.63× | 0.74× | 1.42× | 0.87× |
| `uniq` | uniq over 4M lines in groups of 4 | 126.32 ± 8.71 | 140.27 ± 16.68 | 123.64 ± 8.88 | 512.29 ± 24.08 | 0.98× | 4.06× | 0.88× | 3.65× | 0.90× |
| `uniq` | uniq -c over 4M lines in groups of 4 | 148.96 ± 18.57 | 167.23 ± 17.60 | 141.49 ± 13.50 | 637.71 ± 22.60 | 0.95× | 4.28× | 0.85× | 3.81× | 0.89× |
| `uniq` | uniq -d over 4M lines in groups of 4 | 129.95 ± 16.75 | 140.65 ± 12.55 | 126.84 ± 11.54 | 522.93 ± 27.36 | 0.98× | 4.02× | 0.90× | 3.72× | 0.92× |
| `uniq` | uniq over 4M distinct lines | 114.41 ± 4.19 | 139.33 ± 5.11 | 121.50 ± 4.65 | 994.31 ± 24.27 | 1.06× | 8.69× | 0.87× | 7.14× | 0.82× |
| `uniq` | uniq -u over 4M distinct lines | 120.43 ± 14.28 | 142.82 ± 12.94 | 130.54 ± 3.16 | 980.70 ± 24.27 | 1.08× | 8.14× | 0.91× | 6.87× | 0.84× |
| `uniq` | uniq over 2M 44-byte distinct lines | 105.69 ± 9.55 | 191.93 ± 5.36 | 182.46 ± 9.19 | 645.06 ± 14.21 | 1.73× | 6.10× | 0.95× | 3.36× | 0.55× |
| `uniq` | uniq -f1 -c over 4M lines | 261.98 ± 14.82 | 360.42 ± 20.45 | 220.09 ± 14.68 | 677.57 ± 20.60 | 0.84× | 2.59× | 0.61× | 1.88× | 0.73× |
| `uniq` | uniq from a pipe | 135.93 ± 4.80 | 147.28 ± 6.44 | 215.16 ± 10.87 | 558.05 ± 17.66 | 1.58× | 4.11× | 1.46× | 3.79× | 0.92× |
| `head` | head -n 10 of a 62 MiB file | 0.34 ± 0.14 | 0.32 ± 0.14 | 1.32 ± 0.40 | 2.27 ± 0.21 | 3.92× | 6.74× | 4.15× | 7.14× | 1.06× |
| `head` | head -n 4000000 of a 62 MiB file | 4.42 ± 0.84 | 4.38 ± 0.62 | 22.85 ± 1.27 | 36.06 ± 1.92 | 5.17× | 8.15× | 5.22× | 8.23× | 1.01× |
| `head` | head -c 32M of a 62 MiB file | 3.89 ± 0.54 | 3.81 ± 0.48 | 6.16 ± 0.54 | 2.79 ± 0.39 | 1.59× | 0.72× | 1.62× | 0.73× | 1.02× |
| `head` | head -n 10 from a pipe | 1.44 ± 0.39 | 1.42 ± 0.39 | 1.46 ± 0.46 | 2.54 ± 0.49 | 1.02× | 1.77× | 1.03× | 1.78× | 1.01× |
| `head` | head -n -10 of a 62 MiB file | 7.12 ± 0.83 | 6.56 ± 0.48 | 11.13 ± 1.24 | 3.29 ± 0.42 | 1.56× | 0.46× | 1.70× | 0.50× | 1.09× |
| `head` | head -c -10 of a 62 MiB file | 7.46 ± 1.55 | 6.50 ± 0.69 | 11.40 ± 1.30 | 3.31 ± 0.65 | 1.53× | 0.44× | 1.75× | 0.51× | 1.15× |
| `head` | head -n -10 from a pipe | 27.96 ± 1.47 | 95.38 ± 5.15 | 137.22 ± 5.86 | 1710.71 ± 27.99 | 4.91× | 61.18× | 1.44× | 17.94× | 0.29× |
| `head` | head -c -10 from a pipe | 24.45 ± 1.50 | 19.32 ± 1.40 | 36.94 ± 3.82 | 13.94 ± 2.09 | 1.51× | 0.57× | 1.91× | 0.72× | 1.27× |
| `tail` | tail -n 10 of a 62 MiB file | 0.30 ± 0.13 | 0.32 ± 0.11 | 1.30 ± 0.43 | 2.37 ± 0.50 | 4.29× | 7.84× | 4.01× | 7.32× | 0.93× |
| `tail` | tail -n 4000000 of a 62 MiB file | 46.37 ± 1.95 | 39.43 ± 1.89 | 41.24 ± 1.36 | 27.27 ± 1.77 | 0.89× | 0.59× | 1.05× | 0.69× | 1.18× |
| `tail` | tail -c 32M of a 62 MiB file | 5.37 ± 0.83 | 5.00 ± 0.74 | 6.18 ± 0.61 | 2.79 ± 0.50 | 1.15× | 0.52× | 1.24× | 0.56× | 1.07× |
| `tail` | tail -n 10 from a pipe | 31.54 ± 2.60 | 31.01 ± 3.23 | 130.22 ± 7.43 | 31.74 ± 2.37 | 4.13× | 1.01× | 4.20× | 1.02× | 1.02× |
| `tail` | tail -n +4000000 of a 62 MiB file | 9.48 ± 1.17 | 8.92 ± 0.89 | 42.08 ± 1.79 | 49.00 ± 2.21 | 4.44× | 5.17× | 4.72× | 5.50× | 1.06× |
| `comm` | comm over 2M + 1M sorted lines | 187.28 ± 13.06 | 213.08 ± 6.27 | 149.66 ± 7.48 | 495.30 ± 22.72 | 0.80× | 2.64× | 0.70× | 2.32× | 0.88× |
| `comm` | comm -12 over 2M + 1M sorted lines | 175.10 ± 12.22 | 188.30 ± 17.80 | 120.48 ± 6.20 | 289.07 ± 23.43 | 0.69× | 1.65× | 0.64× | 1.54× | 0.93× |
| `comm` | comm --total over 2M + 1M sorted lines | 186.07 ± 6.00 | 214.33 ± 6.96 | 148.06 ± 5.51 | 480.57 ± 12.28 | 0.80× | 2.58× | 0.69× | 2.24× | 0.87× |
| `comm` | comm of a 2M-line file with itself | 171.64 ± 5.43 | 205.44 ± 9.11 | 171.08 ± 6.18 | 513.46 ± 23.44 | 1.00× | 2.99× | 0.83× | 2.50× | 0.84× |
| `comm` | comm -123 of a 2M-line file with itself | 142.90 ± 5.41 | 137.12 ± 3.31 | 134.41 ± 3.84 | 100.16 ± 8.55 | 0.94× | 0.70× | 0.98× | 0.73× | 1.04× |
| `join` | join two 1M-line files | 465.07 ± 39.84 | 555.83 ± 26.01 | 345.34 ± 12.93 | 259.90 ± 15.92 | 0.74× | 0.56× | 0.62× | 0.47× | 0.84× |
| `join` | join with half unpairable | 390.51 ± 12.87 | 454.76 ± 21.22 | 299.53 ± 7.88 | 222.13 ± 16.98 | 0.77× | 0.57× | 0.66× | 0.49× | 0.86× |
| `join` | join -a1 -a2 two 1M-line files | 481.81 ± 32.09 | 601.07 ± 25.53 | 360.26 ± 10.24 | 238.26 ± 18.34 | 0.75× | 0.49× | 0.60× | 0.40× | 0.80× |
| `join` | join -o 0,1.2,2.3 two 1M-line files | 402.39 ± 24.24 | 480.04 ± 17.10 | 301.77 ± 12.33 | 246.60 ± 9.20 | 0.75× | 0.61× | 0.63× | 0.51× | 0.84× |
| `join` | join -v1 two 1M-line files | 373.34 ± 18.85 | 427.10 ± 22.25 | 277.08 ± 15.28 | 210.13 ± 5.22 | 0.74× | 0.56× | 0.65× | 0.49× | 0.87× |
| `sum` | sum a 62 MiB file | 152.68 ± 13.89 | 242.49 ± 5.64 | 134.36 ± 2.39 | 63.00 ± 3.17 | 0.88× | 0.41× | 0.55× | 0.26× | 0.63× |
| `sum` | sum -s a 62 MiB file | 58.07 ± 2.48 | 34.77 ± 2.69 | 14.29 ± 1.57 | 22.60 ± 1.68 | 0.25× | 0.39× | 0.41× | 0.65× | 1.67× |
| `sum` | sum a 62 MiB file from a pipe | 156.01 ± 3.64 | 250.80 ± 5.51 | 151.43 ± 5.48 | 119.99 ± 3.76 | 0.97× | 0.77× | 0.60× | 0.48× | 0.62× |
| `sum` | sum -s a 62 MiB file from a pipe | 68.79 ± 5.68 | 45.27 ± 4.05 | 25.91 ± 3.11 | 46.21 ± 4.83 | 0.38× | 0.67× | 0.57× | 1.02× | 1.52× |
| `sum` | sum a small file | 0.27 ± 0.12 | 0.28 ± 0.12 | 1.30 ± 0.27 | 2.27 ± 0.48 | 4.78× | 8.34× | 4.59× | 8.00× | 0.96× |
| `sum` | sum 500 small files | 3.75 ± 0.92 | 5.55 ± 1.22 | 4.16 ± 0.72 | 4.93 ± 0.76 | 1.11× | 1.31× | 0.75× | 0.89× | 0.68× |
| `od` | od default of a 62 MiB file | 1006.02 ± 34.44 | 1384.84 ± 89.22 | 4276.90 ± 74.79 | 4383.06 ± 91.48 | 4.25× | 4.36× | 3.09× | 3.17× | 0.73× |
| `od` | od -t x1 of a 62 MiB file | 881.37 ± 42.31 | 1089.60 ± 47.44 | 7654.12 ± 162.10 | 6730.85 ± 117.57 | 8.68× | 7.64× | 7.02× | 6.18× | 0.81× |
| `od` | od -t x1 -w64 of a 62 MiB file | 650.54 ± 26.54 | 740.18 ± 33.26 | 7460.58 ± 134.18 | 5666.84 ± 887.64 | 11.47× | 8.71× | 10.08× | 7.66× | 0.88× |
| `od` | od -t x8 of a 62 MiB file | 744.26 ± 33.63 | 782.41 ± 22.79 | 1245.66 ± 44.14 | 2181.77 ± 55.47 | 1.67× | 2.93× | 1.59× | 2.79× | 0.95× |
| `od` | od -c of a 62 MiB file | 1169.34 ± 32.11 | 1575.50 ± 22.90 | 5439.63 ± 88.72 | 6309.63 ± 74.62 | 4.65× | 5.40× | 3.45× | 4.00× | 0.74× |
| `od` | od -A n -t x1 of a 62 MiB file | 700.38 ± 29.54 | 860.43 ± 19.55 | 7635.88 ± 136.80 | 6545.27 ± 140.30 | 10.90× | 9.35× | 8.87× | 7.61× | 0.81× |
| `od` | od -S 4 of a 62 MiB file | 198.00 ± 10.90 | 263.66 ± 14.91 | 228.64 ± 12.70 | 4438.92 ± 101.03 | 1.15× | 22.42× | 0.87× | 16.84× | 0.75× |
| `od` | od -t f8 of a 1 MiB file | 3951.96 ± 86.86 (15 runs) | 3493.82 ± 125.98 (15 runs) | 181.16 ± 17.02 (15 runs) | 76.27 ± 3.32 (15 runs) | 0.05× | 0.02× | 0.05× | 0.02× | 1.13× |
| `seq` | seq 1 1000000 | 4.00 ± 0.59 | 7.79 ± 1.67 | 9.00 ± 1.29 | 459.82 ± 30.82 | 2.25× | 114.85× | 1.16× | 59.00× | 0.51× |
| `seq` | seq -s, 1 1000000 | 4.10 ± 1.08 | 8.23 ± 4.67 | 8.86 ± 0.79 | 292.86 ± 21.36 | 2.16× | 71.51× | 1.08× | 35.59× | 0.50× |
| `seq` | seq -w 1 1000000 | 3.97 ± 0.54 | 8.21 ± 1.17 | 237.97 ± 12.23 | 469.19 ± 30.52 | 59.92× | 118.13× | 28.98× | 57.13× | 0.48× |
| `seq` | seq 0 0.001 1000 | 3.92 ± 0.51 | 8.40 ± 1.45 | 216.44 ± 13.73 | 529.61 ± 31.97 | 55.23× | 135.15× | 25.76× | 63.03× | 0.47× |
| `seq` | seq -f %.3f 0 0.001 1000 | 3.91 ± 0.56 | 9.21 ± 2.38 | 231.95 ± 28.74 | 508.96 ± 10.73 | 59.35× | 130.23× | 25.19× | 55.29× | 0.42× |
| `seq` | seq 1 2 1000000 | 2.57 ± 0.45 | 8.06 ± 1.04 | 5.45 ± 0.63 | 242.03 ± 21.59 | 2.12× | 94.24× | 0.68× | 30.03× | 0.32× |
| `seq` | seq 1000000 -1 1 | 38.23 ± 5.73 | 50.59 ± 6.96 | 224.38 ± 10.16 | 450.23 ± 19.68 | 5.87× | 11.78× | 4.44× | 8.90× | 0.76× |

### Audit of the open perf issues, 2026-09-22, Linux x86-64 (GNU coreutils 9.12, uutils 0.0.24)

Every utility an open perf issue names, against the pinned oracle rather
than the container's 9.4: a locally built GNU 9.12, Debian's uutils 0.0.24,
both compilers with #9991's `repeat` in the tree, `-O`, a 4-core container.
The `od`, `sort` and `tsort` rows ran with nothing else on the machine; `seq`
through `expr` overlapped a corpus run, so read those within ±15%. Ratios
above 1 mean Fern is faster; `fern-sh` is the self-host compiler's build.

| utility | workload | fern (ms) | fern-sh (ms) | gnu (ms) | uutils (ms) | gnu / fern | gnu / fern-sh |
|---|---|---:|---:|---:|---:|---:|---:|
| `seq` | `seq 1 1000000` | 3.86 | 7.13 | 8.17 | 434 | 2.12× | 1.15× |
| `seq` | `seq -w 1 1000000` | 3.90 | 7.83 | 228 | 440 | 58× | 29× |
| `seq` | `seq 0 0.001 1000` | 3.87 | 7.82 | 211 | 508 | 55× | 27× |
| `seq` | `seq 1 2 1000000` | 2.41 | 7.69 | 5.09 | 225 | 2.11× | 0.66× |
| `seq` | `seq 1000000 -1 1` | 45.5 | 50.0 | 211 | 438 | 4.64× | 4.23× |
| `dircolors` | a 200k-entry config | 47.0 | 45.2 | 35.8 | 102 | 0.76× | 0.79× |
| `dircolors` | `--print-ls-colors` a 200k-entry config | 44.4 | 50.2 | 53.9 | 118 | 1.21× | 1.07× |
| `numfmt` | 200k lines from stdin | 168 | 120 | 87.3 | 128 | 0.52× | 0.73× |
| `numfmt` | 5000 operands | 5.69 | 5.14 | 3.45 | 8.53 | 0.61× | 0.67× |
| `cat` | a 62 MiB file | 5.11 | 4.69 | 1.99 | 3.39 | 0.39× | 0.42× |
| `cat` | `-n` a 62 MiB file | 371 | 483 | 178 | 1792 | 0.48× | 0.37× |
| `cat` | `-A` a 62 MiB file | 312 | 347 | 96.1 | 1959 | 0.31× | 0.28× |
| `join` | two 1M-line files | 512 | 529 | 348 | 258 | 0.68× | 0.66× |
| `join` | `-v1` two 1M-line files | 430 | 422 | 268 | 209 | 0.62× | 0.63× |
| `sum` | a 62 MiB file | 156 | 239 | 130 | 62.2 | 0.84× | 0.54× |
| `sum` | `-s` a 62 MiB file | 40.8 | 34.6 | 17.0 | 20.5 | 0.42× | 0.49× |
| `expr` | `:` anchored class over 4000 bytes | 4.64 | 3.83 | 1.30 | 2.29 | 0.28× | 0.34× |
| `expr` | `:` counts a 4000-byte match | 1.96 | 1.72 | 1.63 | 2.30 | 0.83× | 0.95× |
| `od` | `-t x1` of a 62 MiB file | 929 | 1060 | 7512 | 6624 | 8.09× | 7.08× |
| `od` | `-t x8` of a 62 MiB file | 797 | 791 | 1232 | 2163 | 1.55× | 1.56× |
| `od` | `-t f8` of a 1 MiB file | 4015 | 3342 | 171 | 81.7 | 0.04× | 0.05× |
| `ptx` | 120k words | 373 | 323 | 42.9 | exit 1 | 0.12× | 0.13× |
| `ptx` | `-G` 120k words | 560 | 585 | 58.6 | 188 | 0.10× | 0.10× |
| `ptx` | `-W` a regexp alphabet | 877 | 1742 | 230 | exit 1 | 0.26× | 0.13× |
| `ptx` | prose with sentences | 173 | 153 | 32.7 | exit 1 | 0.19× | 0.21× |
| `tsort` | a 100k-edge DAG from a file | 54.3 | 34195 | 102 | 563 | 1.88× | 0.00× |

Reading it against the issues:

- **`seq` (#8530) is 2–58x faster than GNU natively and 1.15–29x under the
  self-host**; the issue's "40x slower" is stale, and the one loss is the
  self-host build of `seq 1 2 1000000` at 0.66x, which is startup-sized.
- **`cat` of a plain file is 0.39x** and no issue tracks it. GNU 9.12's
  `cat` to `/dev/null` is 2 ms for 62 MiB, which is not a read of the file
  — it is a `copy_file_range` / skip path (#9309) — so this row is measuring
  a primitive Fern does not have rather than its copy loop, and the
  workload should write somewhere that forces a real copy before the
  number is believed.
- **uutils 0.0.24's `ptx` refuses every GNU-extension row** (`GNU extensions
  not implemented yet`, exit 1, in two milliseconds), and the bench used to
  time that refusal as the work; a cell now says `exit 1` where an
  implementation's exit status differs from GNU's on the same workload.
  `ptx` itself was also silently absent from every earlier run — its
  workload file built the prose with `yes | head` under the script's
  `pipefail`, and the SIGPIPE ended the sourcing before a row was measured.
- **The self-host build is within ±25% of native on every row but four**:
  `seq`'s startup rows, `sum` over 62 MiB (the digest byte loop),
  `ptx -W` (`lib/bre.fern` under the self-host, 0.50x) and `tsort` (630x,
  the association-list map, #9608). After #9931's borrow inference the
  self-host column is no longer broadly 2x.

**`sort`, the same day, after finding each line's key spans once.** The
audit's worst row was `sort -k2,2n` at 0.06x: `keycompare` located the key
in both lines on every comparison, and at this compiler's cost per byte
read that walk was 59% of the run (`limfield` 40%, `begfield` 19%). With
keys the spans are now found once per line and the merge sorts line indices
against them; without keys the packed lines sort as before, because the
index indirection alone cost the unkeyed sort 15%. Same machine, same run:

| workload | fern before | fern after | fern-sh after | gnu | uutils | gnu / fern |
|---|---:|---:|---:|---:|---:|---:|
| `sort` 500k lines | 386 | 395 | 339 | 101 | 101 | 0.26× |
| `sort -n` 500k numbers | 1173 | 1327 | 1106 | 154 | 207 | 0.12× |
| `sort -k2,2n` 500k lines | 2688 | 1323 | 1114 | 158 | 230 | 0.12× |
| `sort -k1,1` 500k lines | 1102 | 785 | 722 | 116 | 153 | 0.15× |
| `sort -u` 500k lines | 395 | 400 | 363 | 116 | 113 | 0.29× |
| `sort -c` a sorted 500k-line file | 33.0 | 32.7 | 26.7 | 10.2 | 19.2 | 0.31× |
| `sort -m` two sorted files | 142 | 144 | 144 | 40.3 | 89.1 | 0.28× |

What remains on the numeric rows is the number comparison itself
(`magcompare` 35%, `numcompare` 18% of `-k2,2n`), per comparison by design,
and on every row the per-byte cost of `cmp_bytes` — #8822.

### fmt, 2026-09-22, Linux x86-64 (GNU coreutils 9.12, uutils 0.0.24)

`fmt` was quadratic in the size of a PARAGRAPH (#9983): 40 000 lines with no
blank line between them took 22 s against GNU's 14 ms, and a 4 MB file did
not finish. The chooser was not the cause — its inner walk stops at the
width — the reader was: the paragraph's words lived on the `Para` record,
which the `State` record held while `feed` appended a line's words to them,
so the array was shared at every line's first append and copied whole. The
words are a field of `State` now, `feed` takes the state by ownership, and
the append happens inside the rebuild of that record
(`words: scan_body(s.words, …)`), which is the one shape the compiler moves
rather than copies — an `own` parameter's fields read into locals are still
held by the parameter until scope exit, so the plain `var words = p.words`
move-out idiom does not reach it. 40 000 lines is 63 ms natively and 68 ms
under the self-host build, both linear, both byte-identical to before.

The bench gained the shape, and the corpus a 100 000-line paragraph under a
five-second bound (the old build takes 42 s on it), laid out from 36-column
words so the layout is forced and GNU's 997-word window cannot move a break.
Both compilers, a 4-core container with nothing else on it, ≥20 runs:

| utility | workload | fern (ms) | fern-sh (ms) | gnu (ms) | uutils (ms) | gnu / fern | uutils / fern | gnu / fern-sh | uutils / fern-sh |
|---|---|---|---|---|---|---|---|---|---|
| `fmt` | fmt (default) of a 40 MiB file | 1922.99 ± 39.82 | 1851.52 ± 61.15 | 420.58 ± 18.97 | 462.98 ± 40.35 | 0.22× | 0.24× | 0.23× | 0.25× |
| `fmt` | fmt -w 40 of a 40 MiB file | 1758.44 ± 59.04 | 1727.67 ± 38.72 | 392.03 ± 16.50 | 522.29 ± 54.43 | 0.22× | 0.30× | 0.23× | 0.30× |
| `fmt` | fmt -s -w 40 of a 40 MiB file | 1744.30 ± 54.19 | 1736.51 ± 35.33 | 388.53 ± 14.06 | 527.29 ± 33.48 | 0.22× | 0.30× | 0.22× | 0.30× |
| `fmt` | fmt -u -w 40 of a 40 MiB file | 1754.28 ± 46.65 | 1693.43 ± 43.17 | 394.45 ± 17.68 | 489.13 ± 23.59 | 0.22× | 0.28× | 0.23× | 0.29× |
| `fmt` | fmt (default) of a 38 MiB wrapped file | 2033.05 ± 59.15 | 1928.09 ± 49.87 | 469.86 ± 25.29 | 574.59 ± 29.69 | 0.23× | 0.28× | 0.24× | 0.30× |
| `fmt` | fmt -c -w 60 of a 38 MiB wrapped file | 1929.42 ± 44.53 | 1835.30 ± 54.24 | 435.78 ± 20.60 | 586.28 ± 26.48 | 0.23× | 0.30× | 0.24× | 0.32× |
| `fmt` | fmt -p "" -w 40 of a 38 MiB wrapped file | 1721.31 ± 58.36 | 1639.67 ± 51.50 | 398.08 ± 16.45 | 664.59 ± 20.61 | 0.23× | 0.39× | 0.24× | 0.41× |
| `fmt` | fmt (default) from a pipe | 1928.67 ± 52.25 | 1831.50 ± 32.02 | 535.83 ± 29.21 | 504.60 ± 17.97 | 0.28× | 0.26× | 0.29× | 0.28× |
| `fmt` | fmt (default) of one 200k-line paragraph | 315.10 ± 21.29 | 345.60 ± 14.11 | 56.41 ± 7.35 | 157.53 ± 9.15 | 0.18× | 0.50× | 0.16× | 0.46× |

**fmt does not meet requirement 2.** The last row was "did not finish"
before and is 0.18x now; the prose rows are where they were, 0.22x, and the
self-host build is level with native on every one, so this is not an rc
gap. Under callgrind on 2 MB of prose the chooser is 48% of the
instructions at ~1 450 per word, the word reader 13% and the classifier
10%: the chooser's inner turn reads four tables and each indexed read is
8–10 instructions here where C's is one, which is #8822's x86-64 emitter
and not a shape in `fmt.fern`.

Two reference facts the corpus now pins, because both were wrong in this
tree's own comments until the 9.12 oracle said otherwise: **GNU 9.12 lets a
line reach WIDTH columns exactly** (`fmt -w 20` keeps a 20-column line
whole) where 9.4 held it to WIDTH-1, and the chooser here matched 9.12 all
along. And the bench's uutils column was silently absent on a host with
Debian's rust-coreutils 0.0.24, whose `--list` refusal it read as the list
of names; that is fixed in the same change.

### Conditions as branch chains, 2026-09-22, Linux x86-64 (native compiler)

The first slice of #8822, the x86-64 emitter's per-byte cost. Two shapes
sat in every scan loop. A statement's `&&` or `||` condition lowered to a
typed `if` that materialised the boolean (`setcc`, `movzx`, a push on each
arm, a pop and a second `test` for the branch that consumed it) — eleven
instructions where two compare-and-branch pairs do. And a string or array
index saved the base on the operand stack while the index was loaded,
copied to `rcx` and zero-extended, then restored it — six instructions
where two loads do.

The condition is now lowered in `internal/ir`, so every backend gets it:
`if`, `while` and `for` conditions go through `condBr`, which turns `&&`,
`||` and `!` into a chain of `br_if`s (a block where the operator's own
value is what is branched on), and each backend already fuses a comparison
with the branch that follows it. A coverage build keeps the expression
form, whose arms carry the `&&` / `||` counters. The self-hosted
compiler's semantic lowering builds the same chains (`semsource.cond`);
its register backend already kept a boolean out of memory, so there the
saving is 1 to 3% (`uniq` 178.3 M to 173.0 M instructions, `fmt` 2.44 G
to 2.41 G). The index shape is the
x86-64 peephole: P12 folds the zero-extending copy into a 32-bit move, P5
then writes the index load straight to `ecx`, and P8 now sees a reload
through the compare-and-branch pairs a chain leaves between a store and
its reload.

The second slice is the index itself. After the first, `magcompare` in
`sort -n` still paid eight instructions to read one byte: the SSO tag test
and its branch, the length load, the bounds compare and its branch, the
address `lea`, a `jmp` over the inline-string arm, then the load. The
helper's common path is now straight-line: the bounds check is one compare
against the length in memory and a branch to an abort that sits after the
function's `ret`, the inline-string arm sits there too and rejoins at the
`lea` with `scratch + 1` in the base register, and P13 folds the `lea`
into the load that follows it. That is five instructions for the byte, and
three for an array element. The same slice lets P8 see a reload through
the register loads a compare needs, P11 see a dead reload through a fused
increment, and P14 take a binary operation's right operand from its frame
slot (`cmp eax, dword ptr [rbp-16]`) instead of loading it into rcx first.

Instructions retired under callgrind, one build of each utility from the
same tree at each step, outputs byte-identical on every row:

| workload | before (Ir) | after slice 1 | after slice 2 | change |
|---|---:|---:|---:|---:|
| `sort` 100k lines | 577,720,322 | 542,819,828 | 515,346,655 | -10.8% |
| `sort -n` 100k numbers | 1,949,987,576 | 1,586,532,461 | 1,393,923,126 | -28.5% |
| `sort -k2,2n` 100k lines | 2,125,121,727 | 1,703,001,250 | 1,499,262,993 | -29.5% |
| `fmt` 1.1 MB of prose | 473,948,933 | 425,377,262 | 383,432,551 | -19.1% |
| `cat -A` 3 MB | 142,710,846 | 114,794,139 | 94,250,442 | -34.0% |
| `cat -n` 3 MB | 47,520,260 | 44,179,989 | 43,784,925 | -7.9% |
| `ptx` 300 kB of prose | 955,048,228 | 853,437,426 | 770,658,577 | -19.3% |
| `wc -w` 4.5 MB | 274,461,474 | 239,241,002 | 208,398,983 | -24.1% |

Those rows are `-O -g` builds, which carry every peephole except the ones
a statement boundary's `.loc` directive blocks (#10016); the release build
without `-g` sits 1–3% lower on each and moves by the same fractions.

The third slice is the condition an inlined predicate leaves behind.
`wc`'s `if (is_print(b))` reaches the backend as the callee's
`b >= 32 && b < 127` in the lowering's value form — a typed `if` that
pushes a 0/1 — because the builder's branch chains only see a condition
that is spelled with `&&` at the call site. `ir.ChainConditions` runs
after inlining and rewrites any `&&` / `||` / `!` value that an `if` or a
`br_if` consumes into the same chain, finding the value's extent by
simulating the operand stack. Release builds, outputs identical:

| workload | after slice 2 (Ir) | after slice 3 | change |
|---|---:|---:|---:|
| `sort -n` 100k numbers | 1,365,438,158 | 1,326,683,955 | -2.8% |
| `sort -k2,2n` 100k lines | 1,466,846,688 | 1,400,641,173 | -4.5% |
| `fmt` 1.1 MB of prose | 373,086,326 | 361,039,845 | -3.2% |
| `ptx` 300 kB of prose | 768,177,949 | 748,726,899 | -2.5% |
| `wc -w` 4.5 MB | 203,820,844 | 180,557,068 | -11.4% |

Against the tree before any of the three, the release build of `wc -w`
retires 32% fewer instructions, `sort -n` 31%, `cat -A` 29%, `ptx` 21%,
`fmt` 22%.

The fourth slice is the call. A call with more than two arguments saved
each one on the operand stack and popped them back into their registers
in front of the call — nine instructions around `cmp_bytes`'s call into
`__fern_mismatch`. P15 pairs each push with the pop in the mirror position
and writes the materialisation to that register directly; the window it
needs is 18 lines, so `peepWindow` grew from 10. Release builds, outputs
identical: `sort` 512,307,985 → 471,663,517 (-7.9%), `sort -k2,2n` -2.2%,
`sort -n` -1.5%, `fmt` -1.3%, `ptx` -0.9%. `cat -A` went the other way,
96,428,023 → 98,239,652, with every changed line in its hot function
shorter: the driver's assembler pads every branch that would cross a
32-byte line with NOPs (the Skylake JCC erratum, `relax.go`'s
`branchPad`), and where the padding lands is layout luck. The same two
listings assembled by GNU `as`, which pads nothing, retire 93,060,668 and
91,689,024. The executed padding is 6–7% of `cat -A`'s instructions and
its own issue (#10017).

The fifth is the reload P10 leaves when the statement it fused ends a
block: `add qword ptr [i], 1 / mov rax, [i] / jmp .LblkEnd_9` is what
`i = i + 1; continue;` and every `if` arm ending in an assignment left,
and the accumulator is dead on every edge into a scope label, so P16
drops the load before a jump to one. Release builds, outputs identical:
`cat -A` 98,239,652 → 95,330,849 (-3.0%), `wc -w` -2.5%, `ptx` -1.2%,
`sort` -1.2%.

The sixth is the bounds check the parser had already proven away. Its
len-bounded loop pass marks `s[i]` in `while (i < s.len())` as in range,
and the IR honoured the mark for arrays only; a string index now takes
`__str_idx_nc`, the same SSO dispatch without the compare against the
length. `wc -w` 176,079,326 → 162,147,106 (-7.9%); the other rows' scan
loops are bounded by something other than the string's own length and
do not qualify. The arm64 backend carries the same index-helper layout
as the second slice — the abort and the inline-string arm after the
epilogue, one compare and a `b.hs` on the common path — measured only
by its shape here, since this container runs arm64 under qemu. The pass also accepts the bound captured in a variable —
`var n = s.len(); … while (i < n)`, which `tr` writes seven times —
when nothing between the capture and the loop, or in the body, assigns
or re-binds either name.

A scan loop's `var c = s[i]; if (c >= 48 && c <= 57)` body is eleven
instructions per byte after all three, from twenty-seven. What it still
pays is the stack machine itself: every local is a frame slot, so the
induction variable is stored and reloaded on each iteration, and the byte
is stored to its slot before the chain compares it from the register. That
is the register allocation the SSA backend does (`docs/SSA-DECISION.md`),
not another peephole.

### ptx's column format, 2026-09-22, Linux x86-64 (GNU coreutils 9.12)

#9083's item 3, the restructure rather than the primitive. `render_dumb`
built each output line in a `u8[]` of the line width — `spaces(width)`
copied to bytes, every field written into it one bounds-checked `.with`
per byte, then `rtrim_bytes` copying it back out through a string and a
slice — and the writer got the string plus its newline. The line is now
streamed straight into the writer's buffer: `layout` walks the fields in
line order once to prove every write lands past the one before it and
inside the width, then once more emitting each piece behind its gap of
spaces, with trailing spaces held back until a non-space byte follows
them, which is the trim the whole line used to get. A layout the check
refuses — any piece that would start before the end of the one before
it, or run past the width; a truncation marker whose position clamps at
zero and overlaps the field it marks is the common one — takes the byte
buffer as before, in the write order that path was defined with. The truncation marker is written
before its field in the streaming order, since that is where it sits in
the line.

Outputs are identical to the previous build across 87 invocations — the
default, `-G -O -T -A -r -R -f -w -g -W -F -S`, `--format=roff`, `-i`
and `-o` with real lists, and their combinations, over three inputs —
and `TestPtxParity` passes under both compilers. Release builds under
callgrind and hyperfine, a 4-core container, 20 runs:

| workload | before (Ir) | after (Ir) | before (ms) | after (ms) | gnu (ms) |
|---|---:|---:|---:|---:|---:|
| `ptx` 300 kB of prose | 731,629,742 | 555,371,349 | 110.8 | 88.4 | 31.2 |
| `ptx` 120k words | 2,405,170,749 | 1,793,939,852 | 276.5 | 209.8 | 44.6 |

What remains on the words row is `word_at`'s binary searches (12.5%),
`norm` (7.7%), `key_less` (6.6%) and the sort (5.4%): the per-read cost
of #8822 again, over ~17 probes per field cut.

### od's floats, 2026-09-22, Linux x86-64 (GNU coreutils 9.12)

`od -t f8` of the bench's 1 MiB file was 0.05x GNU: 3.95 s against 181 ms,
30 µs a double, and the audit above had already named the cost — the
long-double library's per-value formatting. `ftoa` followed gnulib's
ftoastr to the letter, `%.*g` at rising precision until the text read
back, and paid for it three times a value: `dec()` expanded the exact
binary value to decimal through the bignum — for a double near 1e-259,
as the seq text decodes to, that is a 300-digit `to_string` — then
`general` rounded the expansion, then `strtold` parsed the candidate back
through the bignum again. Callgrind put 55% of the run in
`BigInt.to_string` and `__bi_mul_small` alone.

The rewrite keeps GNU's rule exactly and changes how each candidate is
found. `std/float`'s Dragonbox already produces the shortest decimal that
reads back; `shortest_digits()` now hands it out as (significand,
exponent), and its digit count is the first precision `%.*g` can succeed
at, so the search starts there instead of at DBL_DIG. That alone is not
the answer: the shortest decimal is NOT always what GNU prints. 2^-24's
shortest decimal has 16 digits, but the 16-digit rounding of its exact
value is a tie that rounds to even and does not read back, so GNU prints
`5.9604644775390625e-08` where Dragonbox says `…063e-08`; 2^-25 is the
same tie one precision later. Each candidate is therefore still the
nearest p-digit decimal, ties to even, checked for read-back — but
`lib/ld.fern`'s new `round_to` computes it from one product rather than
an expansion: v × 10^s is m × 5^s × 2^(e+s), whose integer part is the
digits, whose remainder against half the unit is the rounding, and whose
remainder against half the format's gap is the read-back. The common
case does not touch the bignum at all: the 128-bit power of ten that
Dragonbox's own table holds (`pow10_hi` / `pow10_lo`, 10^k rounded up
to 128 bits) puts m × 10^s within m units of its low bit, which decides
all three questions unless the fraction lies within that error of a
line — an exact tie, for one — and only then does the bignum run.

Release builds, `od -t f8 rand64k.bin` (8192 doubles) under callgrind,
outputs byte-identical to GNU 9.12 on every input tried: 1 MiB of random
bytes and of seq text at `f8`, `f4`, `fH` and `fB`, and a crafted file of
272 doubles at the ties, the `%g` style boundaries, both ends of the
double's range and the subnormal boundary.

| step | Ir | per double |
|---|---:|---:|
| before | 1,552,709,306 | 189,540 |
| Dragonbox start + exact `round_to` through the bignum | 311,823,377 | 38,064 |
| + the 128-bit fast path | 79,758,039 | 9,736 |
| + `Format` hoisted into `Spec`, no borrowed-buffer `with`, no eager records | 68,761,326 | 8,394 |
| + scalar Dragonbox helpers (a returned tuple is a heap cell) | 65,773,305 | 8,029 |

Wall clock, hyperfine, same machine (GNU 9.12):

| workload | before | after | gnu | gnu / fern |
|---|---:|---:|---:|---:|
| `od -t f8` 1 MiB of seq text (the bench row) | 3898 ms | 139 ms | 186 ms | 1.34× |
| `od -t f8` 1 MiB of random bytes | — | 169 ms | 301 ms | 1.78× |
| `od -t f4` 1 MiB of random bytes | — | 232 ms | 360 ms | 1.55× |

Two things the profile left on the table, both compiler work rather than
od's: the four table-word decodes a value cost 1.2k of the 8k
instructions because the stack machine spills every loop iteration, and
`__int_to_string_u64` divides by 100 with `div` — the backend does not
strength-reduce division by a constant, which every utility that prints
a number pays for. The renderer's byte-at-a-time `with` into a buffer
passed as a plain parameter copied the whole buffer on every write (the
parameter holds a second reference, so copy-on-write fires); `own` on
the parameter is what makes it write in place, and it is worth knowing
before reaching for that shape again.

### sum -s on x86-64, 2026-09-22 (GNU coreutils 9.12)

`sum -s` of 62 MiB was 0.25x GNU on x86-64 and 0.41x under the self-host
build, and the whole run was `__fern_sum_bytes`: the x86-64 body was the
scalar byte loop §3.4 of `docs/ATLAS-PLATFORM-PLAN.md` ships first, where
arm64's had already moved to `uaddlp` / `uadalp`. The x86-64 body now sums
sixteen bytes a step with `psadbw` against a zero register and `paddq` — the
SSE2 pair the scalar body's own comment named, and both already in the
native assembler, which has no VEX forms for the AVX2 equivalents. 3 MB of
text: 3.2 ms → 0.83 ms, GNU 1.9 ms. Byte-identical to GNU 9.12 at every
length straddling a block and on 3 MB of text and of random bytes.

### sort -n's one-word key, 2026-09-22 (GNU coreutils 9.12)

`sort -n` of 500k numbers was 0.15x GNU: `magcompare` and `numcompare`
walked both numbers' digits on every one of the n log n comparisons, at
this compiler's cost per byte read, and were half the run. Each line's
`-n` key is now summarised once into one i64 — the sign, the count of
integer digits past leading zeros, and the first fourteen of those digits,
laid out so a plain comparison of two words agrees with `numcompare`
whenever they differ; equal words (a fraction, digits past fourteen, a
magnitude of zero) leave the answer to `numcompare` as before. The
comparison also now reads the byte spans only when it reaches the
last-resort byte compare. 100k numbers under callgrind: 1.28 G
instructions → 394 M; 500k numbers, wall clock:

| workload | before | after | gnu 9.12 (wall / user) |
|---|---:|---:|---:|
| `sort -n` 500k numbers | 883 ms | 339 ms | 174 ms / 329 ms |

GNU's wall clock there is its threads: single-threaded it does the same
work in 329 ms of CPU. What remains on the Fern side is the merge itself
and the comparison's call overhead, `sort_lines` and `compare_at` being
65% of the instructions — #8822's codegen, not the utility.

### The uniqueness test hoisted out of `.with` loops, 2026-09-22 (native compiler)

`tr`'s translate loop was 35 instructions a byte, and a third of them
were `a = a.with(i, v)` asking on every iteration whether `a` is its own
— the `rc.is_unique` test that guards the copy-on-write. Inside a loop
that answer cannot change after the first write: the copy arm leaves the
local holding a fresh buffer, and a body that only reads the array's
elements, takes its length and writes back through the same sites never
gives it a second owner. `HoistUniquenessGuards` (`internal/ir`) now
moves the whole guard to just before the `loop` when every iteration
reaches the write, and where a write sits under a condition or past the
loop's exit test (a rotated `while`), keeps the guard at the site behind
a flag: cleared before the loop, set by the first write that runs the
guard, read by every later one. The lazy form is what keeps a loop that
rarely writes — `cat -n`'s carry into the next line number — from paying
a per-entry test it never needed: 0.6% there, against 16% off `tr`.

Release builds under callgrind, 300k lines of seq text unless named
(the `fmt` and `ptx` rows are their prose inputs), pass off against on:

| workload | before (Ir) | after | change |
|---|---:|---:|---:|
| `tr 0-9 a-j` | 72,179,880 | 60,244,729 | -16.5% |
| `tr -d 0-4` | 59,743,417 | 57,641,768 | -3.5% |
| `tr -s 0-9` | 72,554,066 | 68,852,435 | -5.1% |
| `seq -w 1 300000` | 10,931,325 | 8,955,058 | -18.1% |
| `sort -n` 100k numbers | 394,407,015 | 392,071,948 | -0.6% |
| `cat -n` | 128,151,458 | 128,923,257 | +0.6% |
| `base64` | 207,722,184 | 208,978,317 | +0.6% |
| `cat -A`, `wc -w`, `nl`, `tac`, `od -t x1`, `fmt`, `ptx` | | | ±0.0% |

`tr 0-9 a-j` and `tr -d 0-4` are 11% and 17% faster in wall clock. The
two rows that grew are loops entered once per line that write once or
never, paying the one flag store and, on the first write, the guard they
always paid. Value semantics are pinned end to end on x86-64, arm64 and
wasm (`TestX86_64WithGuardHoistKeepsValueSemantics` and its siblings): a
buffer another name holds is copied before the first write, a loop that
never writes keeps the alias, a loop that never runs leaves the value as
it found it.

### ptx's roff and TeX lines, keys and cuts, 2026-09-22 (GNU coreutils 9.12)

`ptx -G` (traditional, which is the roff format) and `-O` were 0.10x
GNU, and callgrind put half of the 4.3 G instructions in `roff_body`
appending its output a byte at a time — 14 M `str_append` calls for
120k words — and the roff line then concatenated once more before it
was pushed. Both formats now stream into the writer the way the dumb
format already did: `roff_field` pushes the runs between the quotes it
doubles, `tex_quote` the runs between the specials it escapes, and the
line is pieces behind a macro name.

The sort and the cuts were the rest. `key_less` walked both keywords a
byte at a time through `upper` on every comparison; it is now one
`__mismatch` over a buffer folded once when -f asks, with the key spans
in two flat arrays beside the merge. `word_at` searched all 120k words
from scratch for a cut point that is always within a half line of the
keyword whose index the occurrence loop already had, so each occurrence
carries that index and the search walks a few words from it, and its
pair became one packed i64 rather than a heap cell a call. `norm`
rescanned every field for whitespace to turn into spaces; the buffer is
normalised once and a field is a slice of it, the cut positions being
the same in both.

| ptx -G, 120k words | Ir |
|---|---:|
| before | 4,279,051,953 |
| roff and TeX streamed | 1,770,449,485 |
| keys by mismatch, word_at packed | 1,551,666,107 |
| word_at from the keyword, buffer normalised once | 1,193,604,391 |

| workload | before | after | gnu 9.12 |
|---|---:|---:|---:|
| `ptx -G` 120k words | 532 ms | 138 ms | 58 ms |
| `ptx` 120k words | 193 ms | 124 ms | 45 ms |
| `ptx -T` 120k words | 237 ms | 148 ms | 54 ms |
| `ptx` 300 kB of prose | 88 ms | 68 ms | 30 ms |

Byte-identical to the previous build over 14 option sets on three
inputs, and to GNU 9.12 on the two inputs that carry no equal keywords
(the prose file's are ordered by GNU's pointer tie-break, recorded under
Known divergences). What remains is spread thin: the merge, the
mismatch, the per-occurrence `Fields` record and its six strings, and
the sentence scan.

### A right operand kept out of the operand stack, 2026-09-22 (native x86-64)

Every binary operation in the flat x86-64 emitter saves its left value
with `push rax` while the right one is computed, copies the right one to
`rcx`, pops the left back and combines them. P4 and P5 already folded the
cases where the right operand is one instruction; fmt's `choose` still
carried 33 push/pop pairs around index arithmetic, field loads and
bounds-checked element reads. Two rules take the rest:

- P18 renames the right operand's computation onto the register the copy
  was heading for — `push rax / mov rax, [rbp-456] / sub rax, 1 / mov ecx,
  eax / pop rax` is `mov rcx, [rbp-456] / sub ecx, 1` — when every line
  is register arithmetic on the accumulator that names neither that
  register nor rsp. A 32-bit copy zero-extends, so the last line narrows
  to its 32-bit form where its low half depends only on its inputs' low
  halves, and zero-extends explicitly otherwise. P5's restore-into-rax
  form was the one-line case of this and is gone.
- P17 keeps the left value in `rdx` when the right operand's lines cannot
  be renamed because they use `rcx` themselves (an element read through
  the index helper): `mov rdx, rax / … / add rax, rdx`. It admits only
  instructions with explicit operands — `cqo` and `idiv` write rdx without
  naming it, which the first attempt missed and four string tests caught —
  and a branch into a cold arm. The inline-string arm used `edx` as its
  length scratch and now uses `r8d`, so the saved value survives it.

Release builds under callgrind, outputs byte-identical:

| workload | before | after | change |
|---|---:|---:|---:|
| `fmt` 1.1 MB of prose | 348,973,964 | 336,378,123 | -3.6% |
| `sort -n` 100k numbers | 392,071,379 | 380,208,261 | -3.0% |
| `sort` 100k lines | 457,471,084 | 441,670,145 | -3.5% |
| `ptx -G` 120k words | 1,193,606,927 | 1,161,166,232 | -2.7% |
| `od -t f8` 64 kB | 64,812,637 | 61,204,398 | -5.6% |
| `join` | 987,568,225 | 976,165,756 | -1.2% |
| `cat -A` 3 MB | 120,221,610 | 119,320,164 | -0.7% |

`choose` keeps 23 pairs: a nested operation whose inner one already took
`rdx`, a value stored to two slots, and the pushes around calls.

### fmt's word buffer, 2026-09-22 (GNU coreutils 9.12)

GNU fmt holds a paragraph in a buffer of 1000 words and 5000 bytes of
word text. When the 999th word arrives it lays out the 998 before it as
if the paragraph ended there, writes the lines up to the cheapest line
start after the first (each line further along getting a credit of 9),
and keeps the words from that line on as the paragraph's start; when the
5001st byte arrives it does the same with the complete words, keeping the
partial one, and with nothing complete held it writes the 5000 bytes as
they are and the word goes on from there. The re-entered layout costs its
first line against the length of the line last written, and under -c or
-t takes the reader's own column as the continuation indent, so a flush
inside a paragraph's first line can indent everything after the first
line by the width of the text read so far.

`fmt.fern` laid out the whole paragraph, which is a better layout and a
different one from 999 words on — the whole of the prose bench input is
one paragraph, so every line of it past the 124th differed. `scan_body`
now fills the same buffer, `chunk` is the flush, and `choose` returns the
line costs the split reads. The corpus holds paragraphs of 999, 2501 and
3000 varied words under every mode, a byte-buffer flush mid-word, a word
longer than the buffer with and without words before it, and the two
single-line -c / -t flushes; the old build differs from GNU on all of
them. The prose bench input is now byte-identical to GNU 9.12 at 385 M
instructions against 336 M before: each flush re-lays the lines after
its split (14% more words through `choose`) and the reader carries the
writer and the paragraph through every word.

### sort's word radix, 2026-09-22 (GNU coreutils 9.12)

Every sort row paid n log n calls of the comparison, at about 70
instructions a call plus the merge's own 80, and the one-word numeric
key had only made the call cheaper. Now the first comparison's word,
where it has one, orders the lines with an LSD radix sort
(`coreutils/lib/radix.fern`: four sixteen-bit passes, their histograms
taken in one walk, a pass whose digit is the same on every word
skipped) and the merge with the whole comparison runs only inside the
runs of equal words. A plain numeric key's word is its `numeric_key`; a
byte comparison's is its first eight bytes, big-endian and zero-padded,
which orders as the byte comparison does wherever it separates two
lines (a shorter line with the same bytes is the smaller, and a padded
zero can only tie a real one, which the run's merge settles); a folded
comparison's is those bytes folded. A reversed order takes each digit's
complement, and a signed word's top digit has its sign flipped. A first
key that is general or human numeric, month, version, random or
ignoring bytes has no word and the merge runs over everything, as
before; so does any input under 16384 lines, since the histograms are a
fixed twenty million instructions that a ten-line sort must not pay.
The output loop pushes each line straight into the writer's builder
instead of slicing it into a string and concatenating the terminator.
ptx's occurrence sort takes the same word over the keyword, which on
the 120k-word list (every keyword `wordNNNNNN`, so the first eight
bytes tie a hundred at a time) is 126 → 119 ms; an eleven-bit,
six-pass variant with a second word over bytes 8..15 was tried for it
and cost more than the merge it replaced.

Release builds, this container, 15 runs each; GNU runs its merges on
every core, which is why its user time is above its wall time:

| workload | before | after | gnu 9.12 |
|---|---:|---:|---:|
| `sort` 500k lines | 308 ms | 122 ms | 105 ms |
| `sort -n` 500k numbers | 300 ms | 152 ms | 156 ms |
| `sort -k2,2n` 500k lines | 401 ms | 191 ms | 153 ms |
| `sort -k1,1` 500k lines | 615 ms | 171 ms | 112 ms |
| `sort -u` 500k lines | 312 ms | 126 ms | 115 ms |
| `sort -r` 500k lines | 295 ms | 115 ms | 100 ms |
| `sort -s` 500k lines | 309 ms | 120 ms | 107 ms |
| `sort` a sorted 500k-line file | 154 ms | 97 ms | 46 ms |

Under callgrind `sort -n` is 914 M instructions against 2,087 M, and
`sort` 688 M against 1,203 M; what remains is the radix's own bookkeeping
(a third), the numeric key or the word for every line (a sixth), and
reading, splitting and writing the lines. The corpus gains the word
ties: lines whose first eight bytes are equal, lines shorter than eight
that prefix a longer one, folded case beyond the eighth byte, and the
same under -r, -f, -s, -u, -k and the orderings that have no word — at
a size below the threshold and at 20000 lines, and for ptx 20000
distinct keywords.

### cat's spelling scan, 2026-09-23 (GNU coreutils 9.12)

`__scan_set(s, from, set)` is a kernel on every backend and in both
compilers: the index of the first byte at or after `from` whose entry in
the u8[] `set` is nonzero, or the length. It is scalar, a table read per
byte, which is what a byte set costs without a shuffle-based lookup.
`cat -v`, `-T` and `-A` with no numbering or squeezing now take one
scan per chunk against a stop set that holds the spelled bytes and the
newline, so a line costs one scan and one append instead of a memchr, a
per-byte walk and an append. `cat -A` over the 62 MiB bench file:
291 → 187 ms (GNU 9.12: 101). Byte-identical to GNU under -A, -v, -T,
-e, -t, -vE, -vn, -An and -vs over text, a binary and several files.

The same kernel does not help `tr -d`: on the bench input the kept runs
are one to three bytes, and a scan and an append per run cost more than
the per-byte loop (240 → 301 ms). `buf_push_filtered` below is what
closed it.

### tr's translation and deletion, dd's case tables, 2026-09-23 (GNU coreutils 9.12)

Two kernels join the builder family on every backend and in both
compilers. `buf_push_mapped(b, s, table)` appends `table[c]` for each byte
`c` of `s`, or `c` unchanged when it is past the table's end.
`buf_push_filtered(b, s, drop)` appends each byte whose `drop[c]` is zero,
or that is past the table's end. `io_buffered`'s `write_mapped` and
`write_filtered` wrap them. Before them, `tr SET1 SET2` translated through a
`.with` loop into a fresh `u8[]`, copied that into a string and copied the
string into the output buffer. That was about 30 instructions a byte on the
default x86-64 emitter, and `tr -d` did the same with a branch per byte.
The kernels write straight into the output buffer: the translation four
bytes a turn on x86-64, and the deletion with no branch, storing every byte
and advancing the length only past a kept one.

| workload | before | after | GNU 9.12 |
|---|---:|---:|---:|
| `tr 0-9 a-j` over the 62 MiB bench file | 250 ms | 45 ms | 61 ms |
| `tr -d 0-4` over the same file | 247 ms | 53 ms | 81 ms |
| `tr -cd 0-9` over the same file | 290 ms | 63 ms | 83 ms |
| `dd 8MiB conv=ucase` (`bs=64k`) | 41.8 ms | 14.8 ms | 14.8 ms |

`dd`'s `conv=lcase` and `conv=ucase` use the same kernel for each record.
Both utilities are byte-identical to GNU across their corpora, natively
and when built by the self-host compiler.

### wc's word count, 2026-09-23 (GNU coreutils 9.12)

`__count_runs(s, inside, set)` is a kernel on every backend and in both
compilers: how many runs of bytes whose entry in the u8[] `set` is nonzero
begin in `s`, where `inside` says the byte before `s` was a member. `wc`
counts a word where it ends, at a run of whitespace that begins after a word
byte, so the words of a read are one call over the six isspace bytes, and
its lines stay the SIMD `__count_byte`. The x86-64 loop has no branch but
its exit: it keeps "not a member" as a 0/1 byte, and a run begins where
subtracting the previous flag from the current one borrows. It takes two
bytes a turn into two counts.

| workload | before | after | GNU 9.12 |
|---|---:|---:|---:|
| `wc` over the 62 MiB bench file | 163 ms | 52 ms | 85 ms |
| `wc -w` over the same file | 164 ms | 52 ms | 96 ms |

`wc -L` still walks every byte for the line width.

### cat's spelling as one expansion, 2026-09-23 (GNU coreutils 9.12)

`buf_push_expanded(b, s, table)` joins the builder kernels on every backend
and in both compilers. It appends each byte `c` of `s` as the eight-byte
record at `table[c * 8]`: a length byte (above 7 counts as 7), then the
bytes. A byte whose record is not wholly inside the table passes through.
It reserves room for eight bytes per input byte, so x86-64 copies each
record with one eight-byte store and advances the tail by its length.

`cat -v`, `-T`, `-E` and their combinations, with no numbering or
squeezing, now build one table of every byte's output (`-E`'s `$` on the
newline included) and make one call per read. That replaces the set
scan, per-run appends and per-byte spelling above. `cat -A` over the 62 MiB
bench file: 192 → 74 ms (GNU 9.12: 104). Over 3 MB of random bytes, where
nearly every byte is spelled: 62 → 4.4 ms (GNU: 22.7).

GNU 9.12's `-E` without `-v` shows a carriage return that ends a line as
`^M$`. That CR can end one read, or one file, while its newline starts the
next. A CR that ends the input is copied as it is. Fern's cat had never
done this. The CR is held back until the next byte decides it, and a read
holding a CR under `-E` alone takes the line rewriter instead of the
expansion. The corpus now covers the CRLF line, the CR across files and
reads, and the trailing CR.

### The BSD checksum, 2026-09-23 (GNU coreutils 9.12)

`__bsd_sum(s, sum)` continues the checksum that `sum -r` and `cksum -a bsd`
keep over a string. For each byte it rotates the 16 bits right by one and
adds the byte, modulo 2^16. It is a kernel on every backend and in both
compilers, and `std/hash`'s `BsdSum.update` calls it. The checksum is one
serial chain through the sum, and the default x86-64 emitter spent about
fifteen instructions a byte on it. The kernel's loop spends two on the
chain, a `ror` and an `add` on the 16-bit register.

| workload | before | after | GNU 9.12 |
|---|---:|---:|---:|
| `sum` of the 62 MiB bench file | 191 ms | 49 ms | 111 ms |
| `cksum -a bsd` of the same file | 186 ms | 52 ms | 113 ms |

### nl's ordinary lines, 2026-09-23 (GNU coreutils 9.12)

`nl` made every line a string, `slice_unchecked` plus a copy, and ran it
through `proc_line`. That built and dropped `State`, `Style` and `Emit`
records, allocated an `Option` for `checked_add`, and found the field width
with a loop of 64-bit divisions. It cost about 1,750 instructions a line. A
fast path now takes every ordinary line of a read straight from the read
buffer. An ordinary line is not a possible delimiter, has style `a`, `t` or
`n` with no `-l` run to count, and has no overflow pending. Its number
field is cached per hundred: the padding and leading digits (or, for
`-n ln`, the trailing padding and the separator) are formatted when the
number enters a new hundred. Each line then pushes that cached part, a
digit pair from `io_buffered.digit_pairs()`, and a range of the buffer. The
overflow check is two comparisons. `io_buffered.buf_u64_len` counts digits
by comparison rather than division. Regular-expression styles, `-l` runs
and delimiter lines still take `proc_line`.

| workload | before | after | GNU 9.12 |
|---|---:|---:|---:|
| `nl` over the 62 MiB bench file | 2,229 ms | 400 ms | 998 ms |

### cat's plain copy, spliced, 2026-09-23 (GNU coreutils 9.12)

GNU 9.12's `cat` with no formatting option never copies a byte through
userspace. A regular file onto a regular file goes by `copy_file_range`.
Anything else, unless the input is a regular file of 32 KiB or less, goes
by `splice(2)` through a pipe of its own, grown to 512 KiB, with stdout's
pipe grown to match. Fern's `cat` read and wrote 128 KiB at a time, 480
reads and 480 writes for the bench file.

`r.splice_to(w, max)` is that move as a handle method, on every backend and
in both compilers. It tries a direct splice first, which serves any pair
with a pipe on one side, and a writer that is a pipe is grown to hold `max`
(asked once per writer). A pair with no pipe goes through a pipe the runtime
creates on first use and keeps. Before anything leaves the reader, a
non-blocking splice from the empty pipe asks whether the writer takes spliced
bytes, so `Unsupported` always means nothing moved. `cat` then falls back to
reading and writing from the same offset, and any real failure is met on its
own side, with GNU's wording. An appending stdout and `/dev/full` both take
that path. wasm and the interpreter answer `Unsupported` to every call.

`cat` passes 1 MiB, the kernel's default ceiling for a pipe. Against GNU's
512 KiB it measured 3 ms to 4 ms on the bench file in a C loop of the same
two splices. Regular file to regular file goes through the pipe too, rather
than `copy_file_range`; the bench has no such row.

| workload | before | after | GNU 9.12 |
|---|---:|---:|---:|
| `cat` of the 62 MiB bench file | 18.0 ms | 1.94 ms | 3.43 ms |
| `cat` of two 62 MiB files | 29.8 ms | 3.16 ms | 4.98 ms |
| `cat` from a pipe | 18.2 ms | 4.84 ms | 4.67 ms |

Reading from a pipe is set by GNU `cat` on the writing end, and the two runs
are level within their spread (σ 1.2 and 1.4 ms). The self-hosted build is
1.65×, 1.39× and 0.91× GNU on the same rows.

### sort's check, sorted input and output loop, 2026-09-23 (GNU coreutils 9.12)

Three changes, each where `sort` spent time GNU does not.

- **`-c` reads as it goes.** It used to read the whole input, join the reads
  into one string, and split that into a line array before comparing
  anything: 18 ms of system time for the bench file, most of it page faults.
  Now each read is appended to what is left of the one before, the last
  whole line and a partial one, and lines are compared as they are found.
  Memory is one read.
- **Input already in order is returned as it is.** The merge is stable, so
  a non-decreasing input is its own result. GNU's merge finds that in about
  one comparison a line, where the radix pass cost 418 instructions a line
  whatever the order. `in_order` makes the n − 1 comparisons first, and
  unsorted input stops at its first disorder.
- **The output loop pushes into the writer's builder** and flushes once a
  block has built up. It used to rebuild an `Out` and a `BufWriter` for
  every line, about 190 instructions a line.

| workload | before | after | GNU 9.12 |
|---|---:|---:|---:|
| `sort -c`, sorted 500k-line file | 41.1 ms | 17.8 ms | 10.6 ms |
| `sort`, sorted 500k-line file | 173 ms | 46 ms | 57 ms |
| `sort`, 500k shuffled lines | 224 ms | 212 ms | 167 ms |
| `sort -m`, two sorted files | 151 ms | 129 ms | 41.5 ms |

What is left in `-c` is about 260 instructions a line, spread over the line
scan, the comparison and its byte kernel. `-m` streams now (next section).
The shuffled row is GNU's threads: it spends 265 ms of CPU time to finish in
167 ms.

### sort -m streams, 2026-09-23 (GNU coreutils 9.12)

`-m` read every input whole, split the text into a line array and merged
that into a second array before writing a line. For two 6 MB files that is
68 MB resident and 17,000 page faults, which is 36 ms of system time against
GNU's 2. Making the faults cheaper does not help: `MADV_HUGEPAGE` on the
arena cut the faults to 935 and left the system time where it was, because
the cost is the bytes touched, not the number of faults.

The merge now holds one buffer per input, as GNU does. Each input is read
until its buffer holds a whole line, the smallest head line across the
inputs is written, and the input it came from moves to its next line,
refilling only when its buffer runs out. Memory is one read per input, so
`-m` over presorted files larger than memory works, which is what the
option is for. To compare lines held in two different buffers, every
comparator takes a text for each side (`cmp_bytes(ta, a0, a1, tb, b0, b1)`
and the rest); the sort passes its one text twice, at no measurable cost.
`vercmp.compare_range` went with it. Opening the `-o` file empties it, so an
input that is also the output, named or as standard input, is read whole
before the output is opened, which is GNU's copy of it aside.

| workload | before | after | GNU 9.12 |
|---|---:|---:|---:|
| `sort -m`, two sorted 6 MB files | 120 ms | 65.6 ms | 43.9 ms |
| the same, self-hosted build | 141 ms | 99.3 ms | |
| peak RSS | 68 MB | 10 MB | 10 MB |

The per-line state lives in local arrays rather than in the struct the
refill takes. Read out of an `own` struct into locals, the native
compiler keeps the fields shared until the struct's exit drop, so each
`.with` copied the array: 337 instructions a line in `advance` (#10084).

### fmt's layout pass, 2026-09-23 (GNU coreutils 9.12)

`choose` is GNU's dynamic program: for each word, the cheapest way to end a
line there given the best layout of everything before it. It tried every
earlier word as the line's start and recomputed each line's width by
walking the words, and every word was its own record of strings and flags.

- **A word is three i64s** in arrays that one `Words` value holds for the
  whole run: its byte span, its line and flags (period, sentence end,
  punctuation, parenthesis), and its trailing space and gap. The arrays are
  reused from paragraph to paragraph, swapped with a spare so they stay
  uniquely owned.
- **Word ends are found by a table**: `__scan_set` skips a word's bytes in
  blocks, and one byte-class table gives the flags.
- **A line's width is a difference of prefix sums**, and the farthest word a
  line from here can reach moves forward with a pointer, not a walk.
- **Candidates are tried longest line first and pruned**: once a line is no
  longer than the goal, a shorter one only adds shortness cost, so a
  candidate whose cost is already above the best is cut off, with a
  monotone deque holding the best layout cost seen so far.
- The cost weights are constants rather than calls.

Output is byte-identical to GNU on the prose bench input, in 400 random
differential trials, and in the 1490-case corpus on both compilers.

| workload | before | after | GNU 9.12 |
|---|---:|---:|---:|
| `fmt`, 2.8 MB of prose | 162 ms | 79.3 ms | 42.7 ms |
| the same, self-hosted build | 179 ms | 142 ms | |

That is 1287 M instructions down to 675 M. What is left is `choose` and
`scan_body` at about 350 instructions a word between them, most of it the
flat backend's operand traffic.

### wc -L, 2026-09-23 (GNU coreutils 9.12)

`-L` walked every byte to track the display width. `scan_width` now asks
`__scan_set` for the next byte that is not a plain printable one, and adds
the length of the run before it as width. Only tabs, control bytes, line
ends and high bytes are handled one at a time, and the line ends it stops
at are the line count. Without `-L` lines are `__count_byte`, and words are
always `count_words`.

| workload | before | after | GNU 9.12 |
|---|---:|---:|---:|
| `wc -L` of a 62 MiB file | 157 ms | 111 ms | 89.1 ms |
| the same, self-hosted build | 241 ms | 161 ms | |

### join's matched keys and field ends, 2026-09-23 (GNU coreutils 9.12)

Every matched key declared its two group arrays as `[]`, which allocates:
two allocations a pair, although one line on each side, the common shape,
never touches them. They are now declared once and built only when a
second line with the key turns up. A default-mode field now ends at the
first byte of a set holding the three blanks and the line terminator, one
`__scan_set` a field. That replaces a `__memchr` for a space plus the tab
and newline cursors that decided when it could be trusted.

| workload | before | after | GNU 9.12 |
|---|---:|---:|---:|
| join two 100k-line files | 60.6 ms | 53.1 ms | 41.9 ms |

That is 472 M instructions down to 426 M. About 390 of the roughly 2,100
instructions left a line go to allocating and dropping the `Line` and `In`
records each line builds.

### ptx's fields as spans, and bre's end-only matcher, 2026-09-23 (GNU coreutils 9.12)

Each output line used to copy six strings out of the normalised buffer
(tail, before, keyafter, keyword, after, head) for the renderers to print.
`cut_fields` now returns them as spans of that buffer, and the column,
roff and TeX renderers write the spans directly. One `Line` record now
serves every output line instead of two being allocated per line, and
`line_of` runs only when a reference is printed. When a run of equal
leading words is already in order, which a word repeated through a text
is, the occurrence sort checks it with one comparison per element and
skips the merge passes. `word_at` indexes the word table in place: copying
its two arrays into locals cost an inc and a dec per call.

In `lib/bre.fern`, `search_from` skips to the next position a match can
start at with one scan (`__scan_set` over the fastmap, or the literal
prefix search) instead of testing every position. `match_at`, which
`search_from` and `search_back` use and which only needs the match's end,
now runs a thread simulation without captures for any pattern without a
backreference. It keeps flat program-counter lists and one stamp per
position, where the capture engine allocated per thread and per step.
The capture engine now also passes its thread list as `own`, so a stamp
updates it in place. That matters for `exec`, which still needs the
groups.

The compiler's escape summary judged a call argument by whether it
mentioned a parameter at all. So a parameter that was only read (`r.k`
in an `i32` field of a self-recursive call's argument, a byte of a string
stored into a `u8[]`) counted as escaping, became owned, and cost a
retain and a drop on every call. A call argument is now judged against
the callee's declared parameter type, and a `.with` or `.append` element
against the array's element type. ptx's `layout` and `render_dumb`
(through `Opts`) and bre's `add_thread` (through `re`) had been paying
that retain and drop.

| workload | before | after | GNU 9.12 |
|---|---:|---:|---:|
| ptx 120k words | 189.8 ms | 117.6 ms | 57.6 ms |
| ptx -G 120k words | 163.6 ms | 122.3 ms | 75.6 ms |
| ptx -O 120k words | 162.9 ms | 109.0 ms | 69.2 ms |
| ptx -T 120k words | 204.5 ms | 145.9 ms | 72.4 ms |
| ptx -W a regexp alphabet | 891.0 ms | 267.9 ms | 295.2 ms |
| ptx prose with sentences | 110.0 ms | 76.7 ms | 42.5 ms |
| ptx -A prose | 139.3 ms | 98.7 ms | 46.4 ms |

In instructions: 1.28 G to 0.78 G for the 120k words, 6.85 G to 1.88 G
under `-W`, and 656 M to 428 M for the prose. GNU runs the words in
377 M. What is left is spread across the column layout (`put` runs about
100 instructions a call, ten calls a line) and the radix sort's fixed
histogram cost. No single phase dominates.

Two differences from GNU turned up along the way and are fixed. ptx's
default sentence regexp lacked GNU's trailing `[ \t\n]*`, so a sentence
ending at a newline started the next context at the newline, and the
first keyword of the next line printed one column right of GNU's. The
context end also now drops the separator's trailing blanks, as GNU's
does. Since whitespace can be a word byte under `-b`, a context's words
are now the scanned words cut to the context, as GNU's scan finds them,
and the widest word is measured after the cut.

### nl's regex style on the fast path, 2026-09-23 (GNU coreutils 9.12)

`nl -bpRE` sent every line through `proc_line`. That meant a copy of the
line into a string of its own, a call to `fast_lines` that returned at
once with a fresh tuple, the style fetched as a retained `Style`, and the
`State` and `BufWriter` rebuilt in an `Emit` record. The regex style now
takes `fast_lines`' loop like the others do. The line is searched where
it sits, with `search_range` over the read buffer, and a line goes to
`proc_line` only when it might be a delimiter or its number would
overflow. `proc_line` itself works on the buffer range too.

| workload | before | after | GNU 9.12 |
|---|---:|---:|---:|
| nl -bp7 a 3 MiB file | 142.7 ms | 36.5 ms | 85.4 ms |

That is 1.09 G instructions down to 271 M.

Searching the read buffer in place first leaked every 64 KiB buffer read,
because of a gap in the compiler's retain summaries. They are least
fixpoints that start all-false, so a recursive function passing its
parameter back into the same slot (bre's `add_thread` passes the text it
matches in) could never be credited. That refusal travelled up through
`search_range` to nl's loop, where the buffer stayed borrow-tainted and
was never released. A direct self-recursive call's argument in the
parameter's own slot is now credited. Any retention the recursion does
make happens at some other occurrence, which still refutes it.

### Overwrite moves for `x = y`, 2026-09-23 (GNU coreutils 9.12)

comm's merge loop keeps each side's current line and the two lines
before it as string locals, and shifts them along once a line:
`back1a = back1b; back1b = cur1; cur1 = line`. Every one of those
copies retained its source and released the target's old value, and the
source was then overwritten a few statements later, which released it
again. That was 20 M `__fern_str_dec` calls, 400 M of the 1.48 G
instructions `comm -123` ran over a 2M-line file against itself.

The native IR now treats `x = y` as a move when y's next event, on every
path through the rest of its statement list, is a write that does not
read y: an assignment to y, or an if/else each of whose arms begins with
one (`computeOverwriteMoves`). x takes y's reference without a retain,
y's slot is emptied, and the write that ends the dead stretch skips
releasing the empty slot. A break or continue out of the list before
the write, or any read of y, keeps the copy. So do a local a closure
captures, a map, and a string on a target that carries strings as two
words.

| workload | before | after | GNU 9.12 |
|---|---:|---:|---:|
| comm over 2M + 1M sorted lines | 228.6 ms | 195.6 ms | 182.5 ms |
| comm -12 over 2M + 1M sorted lines | 225.1 ms | 196.1 ms | 138.6 ms |
| comm of a 2M-line file with itself | 217.8 ms | 177.4 ms | 202.3 ms |
| comm -123 of a 2M-line file with itself | 197.4 ms | 152.4 ms | 133.6 ms |

`comm -123` went from 1.48 G instructions to 1.21 G (GNU: 1.30 G).

The same move applies when the list that declares y ends without
mentioning y again, since y's scope ends there and a loop coming back
into the list re-runs the declaration first. That also counts when the copy
sits in an if/else arm of the declaring list and nothing after the `if`
names y. uniq's `var src = chunk; ... prev = src;` and comm's
`var line ...; if (...) { cur1 = line; }` are both that shape. A loop
between the declaration and the copy still keeps the copy, because
the next pass reads y again. So does a y that a `var v = y` borrows
without a count: x's next write would free the box v still reads.

| workload | instructions before | after |
|---|---:|---:|
| comm -12 over 2M + 1M sorted lines | 1.50 G | 1.42 G |
| uniq -c over 4M lines | 1.31 G | 1.28 G |
| uniq -f1 over 4M lines | 2.28 G | 2.25 G |

These moves are native-only (#4451), and the self-host compiler needs
none. For every shape in `internal/ir/overwrite_move_test.go` it emits
no retain wherever native now moves the source. Its string copies
retain only a source credited `STRALIASSRC:`, and its IR optimizer
cancels the array retain against the next release.

### uniq -f's field skip by scan, 2026-09-23 (GNU coreutils 9.12)

`uniq -f N` found where each line's compared field starts by walking
blanks and then the field's bytes one at a time, with a separator test
per byte. Each run is now one `__scan_set` against a table built once:
every byte that is not a separator, then the separators and the line
terminator. A scan can pass the line's end, into the next line or to
the end of a carried line, so its answer is clamped to the line's
content, and the offsets come out as the byte walk gave them.

| workload | before | after | GNU 9.12 |
|---|---:|---:|---:|
| uniq -f1 -c over 4M lines | 336.5 ms | 308.4 ms | 253.9 ms |

Over the first 20 MB of that input: 1.17 G instructions to 1.04 G
(GNU: 0.92 G). What is left is the per-line loop itself, about 290
instructions a line, and the retain `var src = chunk` takes. The one
`prev = src` took is gone with the scope-dead move above.

### cat -n's number field in two pushes, 2026-09-24 (GNU coreutils 9.12)

`cat -n` wrote each `%6d\t` field as three pushes: the padded hundreds,
the last two digits from the pair table, and the tab. It also divided
the line number by 100 on every line to find them. The last two digits
and the tab are now one 3-byte slice of a table built once
(`number_tails`), with a second hundred for the first, whose tens are
blank below 10. The hundreds change once in a hundred lines, and the
division happens only then.

| workload | before | after | GNU 9.12 |
|---|---:|---:|---:|
| cat -n over 4M lines (median of 50) | 148.6 ms | 111.8 ms | 101.8 ms |
| cat -b over 4M lines | 156.0 ms | 111.9 ms | 101.9 ms |

Instructions: 1.16 G to 1.04 G (GNU: 0.83 G).

### 64-bit division by a constant, 2026-09-24 (GNU coreutils 9.12)

`od`'s decimal formats (`-tu8`, `-td8`, …) split each value into digit
pairs with `/ 100` on a u64, about eighteen times for a 20-digit value.
Division by a constant took the multiply-high reciprocal only at i32; at
64 bits it was a hardware `div`, 35–90 cycles each. Both x86-64 emitters
now take the reciprocal at 64 bits too: the one-operand `mul` / `imul`
leave the high half of the 128-bit product in rdx.

| workload | before | after | GNU 9.12 |
|---|---:|---:|---:|
| od -An -tu8 over 16 MB of random bytes (median of 15) | 767.4 ms | 497.6 ms | 452.4 ms |

The divide is three instructions and the reciprocal eight, so the
instruction count rises slightly. What changes is the latency on the
dependency chain: each quotient feeds the next division.

`nl` measured 194.8 ms before and 207.5 ms after, with the same
instruction count. Its division runs once per hundred lines, in a
branch inside `fast_lines`' loop. Padding the old build with 18 bytes
of nops at that site (the reciprocal's extra length) gives 210.7 ms. So
the slowdown is where the loop's code lands, not the division. Aligning
every loop head to 16 bytes does not remove that sensitivity: it moved
the old build to 204.6 ms and the new one to 196.7 ms.

### The self-host's builder copies, 2026-09-24 (GNU coreutils 9.12)

The self-hosted compiler's runtime copied a byte at a time in every
byte-buffer and string-builder helper: `buf_push`, `buf_push_range`,
`buf_take`, the reserve that grows a buffer, and the three string-builder
helpers. That is six instructions a byte. On x86-64 they now call a
`__fern_memcpy` with native's size classes, each covered by two
overlapping accesses. `rep movsb` was tried first and made `uniq`
slower (337 ms to 372 ms), because a line push copies a few bytes and
`rep movsb` is nearly all start-up at that length. On arm64 they take
the memcpy op's word loop. Self-host builds, x86-64:

| workload | before | after | GNU 9.12 |
|---|---:|---:|---:|
| uniq over 8M distinct lines | 339.3 ms | 289.3 ms | 284.0 ms |
| sort -m of two sorted 500k-line files | 77.9 ms | 62.7 ms | 40.4 ms |

Instructions: `sort -m` 778.9 M to 518.9 M, `uniq` over the first 16 MB
875.0 M to 714.4 M. Native builds were already copying through their
own `__fern_memcpy`.

### ls, 2026-09-14, Linux x86-64 (GNU coreutils 9.4, uutils 0.0.24)

The same 4-core container, so read the columns against each other. The
`ls-tree` workload is 4000 names in one directory, 1500 mixed entries
(files, directories and symlinks) in another, and a 40-deep tree.

| workload | fern (ms) | gnu (ms) | uutils (ms) | gnu / fern | uutils / fern |
|---|---|---|---|---|---|
| 4000 names, no stat | 4.10 ± 0.47 | 3.53 ± 0.95 | 5.72 ± 0.77 | 0.86× | 1.40× |
| `-l` over 4000 names | 14.03 ± 1.41 | 12.59 ± 1.32 | 14.31 ± 1.69 | 0.90× | 1.02× |
| `-U` (unsorted) over 4000 names | 2.88 ± 0.30 | 2.27 ± 0.21 | 4.56 ± 0.41 | 0.79× | 1.58× |
| `-v` (filevercmp) over 4000 names | 11.64 ± 1.38 | 4.97 ± 0.53 | 22.98 ± 2.30 | 0.43× | 1.97× |
| `-t` over 4000 names | 8.54 ± 1.31 | 6.52 ± 0.84 | 8.18 ± 0.90 | 0.76× | 0.96× |
| `-S` over 4000 names | 8.20 ± 1.22 | 6.63 ± 0.94 | 7.05 ± 0.80 | 0.81× | 0.86× |
| `-C -w 200` over 4000 names | 5.20 ± 0.74 | 4.41 ± 0.65 | 5.54 ± 0.73 | 0.85× | 1.06× |
| `-x -w 200` over 4000 names | 5.22 ± 0.52 | 3.81 ± 0.40 | 5.53 ± 0.56 | 0.73× | 1.06× |
| `-m -w 200` over 4000 names | 4.52 ± 0.48 | 3.77 ± 0.51 | 5.56 ± 0.66 | 0.83× | 1.23× |
| `-i -s` over 4000 names | 8.87 ± 0.92 | 7.02 ± 0.72 | 9.35 ± 1.02 | 0.79× | 1.05× |
| `-F` over 1500 mixed entries | 3.82 ± 0.63 | 2.55 ± 0.44 | 3.71 ± 0.52 | 0.67× | 0.97× |
| `-l` over 1500 mixed entries | 5.69 ± 0.77 | 5.90 ± 0.65 | 7.50 ± 0.96 | 1.04× | 1.32× |
| `--color=always` over 1500 mixed | 3.61 ± 0.45 | 2.87 ± 0.38 | 3.98 ± 0.36 | 0.80× | 1.10× |
| `-R` over a 40-deep tree | 2.48 ± 0.27 | 1.70 ± 0.26 | 3.25 ± 0.47 | 0.69× | 1.31× |
| `-lR` over a 40-deep tree | 3.87 ± 0.55 | 5.23 ± 0.53 | 4.91 ± 0.44 | 1.35× | 1.27× |
| `-b` over 4000 names | 4.93 ± 1.13 | 3.60 ± 0.41 | 6.13 ± 1.06 | 0.73× | 1.24× |
| `--quoting-style=shell-escape` | 4.76 ± 0.77 | 3.50 ± 0.44 | 7.08 ± 0.95 | 0.73× | 1.49× |
| `--time-style=full-iso -l` | 15.12 ± 1.89 | 13.98 ± 4.83 | 15.74 ± 7.04 | 0.92× | 1.04× |

Faster than uutils on thirteen of eighteen rows and faster than GNU on
two. The first run of the port was **0.09× GNU on `-l`** and 0.17× on
`-lR`, and what closed that was four things asking per ENTRY for work
needed once: the owner and group databases were re-read per entry (the
`pwdb` header says its path-taking lookups are "quadratic for one asking
per entry", and a long listing asks twice per entry), the name
comparison was a byte loop where `<` is one `memcmp`, `mode_string`
allocated twelve times per line, and `pad_left` allocated once per byte
of padding. 151 ms to 13.9 on the `-l` row.

What is left is measured and not ls's own:

- **`timefmt` re-parses the format string per call (#9281)** — 8.7% of a
  long listing. A shared lib with seven consumers, five of which format
  per record, so the compiled-format API is its own change.
- **Allocation and refcount churn, ~23%** — what remains after the
  per-entry allocations above: one `to_string()` per number and one
  buffer per line, which is the floor for this shape.
- **The merge sort's bookkeeping, ~14%** — ~100 instructions per merge
  step over an `i32[]` index array, where `dst.with(at, v)` is the step.
  There is no array primitive that writes in place without the
  uniqueness check. It is the whole of the `-U` row's gap and most of
  the plain one's.
- **filevercmp is ~10× GNU's cost per comparison**, which is the `-v`
  row. A backward single-pass `file_prefixlen` was written and MEASURED
  against the forward one and lost by 20%: the names a sort sees have
  one dot near the end, where the forward scan stops at the first dot
  and the backward one walks the whole prefix. The cost is the per-byte
  call, not the shape.

### The fold, 2026-09-14, Linux x86-64 (GNU coreutils 9.4)

`Cksum.update` calls the `__crc32_cksum` kernel now (#9128), so the CRC is
folded sixteen bytes a step with `pclmulqdq` against four accumulators rather
than walked through a table. A third machine again — a 4-core container, not
either box above — so read the columns against each other and not against the
two tables above. 62 MiB of text, 20 runs each, wall time; `uutils` is not
installed here, so that column is absent rather than guessed.

| workload | fern before (ms) | fern after (ms) | gnu (ms) | gnu / fern after |
|---|---|---|---|---|
| `cksum` of a 62 MiB file | 157.5 ± 6.4 | 18.1 ± 1.7 | 16.6 ± 1.5 | 0.92× |
| `cksum` of a 62 MiB file from a pipe | 161.5 ± 6.6 | 19.5 ± 1.5 | 17.8 ± 1.2 | 0.91× |
| `--raw` of a 62 MiB file | 153.0 ± 5.8 | 17.5 ± 1.7 | 15.6 ± 1.2 | 0.89× |
| `cksum` of a small file | 2.04 ± 0.15 | 2.07 ± 0.13 | 3.26 ± 0.13 | 1.57× |

**8.7×, and this host's GNU is folding too** — `cksum --debug` prints "using
pclmul hardware support" — so 0.92× is fold against fold, which is the
comparison #9056 asked for and the one neither table above could make. The
`0.04×` and `0.18×` rows are answered: what was missing was the instruction,
exactly as they said.

The remaining 8% is the tail and the call, not the loop: the kernel reduces
its 128-bit residue by feeding sixteen bytes back through the bit-at-a-time
step rather than a Barrett reduction, and `update` is a call per read block
where GNU inlines. Both are worth what they cost — the residue path needs no
constants and no second reduction — and neither is where the 23× lived.

The small-file row does not move, which is the point of checking it: the
table is still built per hasher (`finish` folds the length through it, and
`update_bytes` has no string to hand the kernel), so nothing about startup
changed in either direction.

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
    priority()                    getpriority(PRIO_PROCESS, 0), bias undone
    set_priority(nice)            setpriority(PRIO_PROCESS, 0, nice)

The five filesystem ones take `fs` and WASI implements every one of them, so
they are provided on wasm rather than classified out. `umask` takes `fsmode`
beside `access` and `write_file_exec` and is refused there — WASI has no
file-mode creation mask, and `path_create_directory` has no mode either, which
is the same fact twice. `docs/FREESTANDING-CORE.md` carries both.

The `priority` pair (#8375's primitive, what `nice` waited on) takes a
capability of its own, `sched`, and is refused on wasm for the reason
`umask` is: neither preview has a scheduler knob, and neither stand-in is
honest — answering 0 from the read claims the default nice value was
measured, and letting the write succeed claims a change that did not happen.
Two ABI facts there are not shareable between backends. The syscall numbers
are SWAPPED between the tables (x86-64 has get at 140 and set at 141; the
asm-generic table aarch64 uses has set at 140 and get at 141), and Linux
returns the nice value BIASED by 20 so a success is never negative where BSD
returns it directly and reports failure through errno alone — so the read
undoes the bias on Linux and not on Darwin, and a copied table silently runs
the other half of the pair.

That split reaches the INTERPRETER too, and Go hides it: `syscall.Getpriority`
is the raw syscall on Linux and libSystem's wrapper on Darwin, so the same
call returns the biased value on one and the nice value on the other. Hence
`priority_linux.go` and `priority_darwin.go` beside `priority_other.go`,
the way `mknod` and `fsstat` already split. The TESTS stay off the split
entirely by reading back a value the probe itself set, which is sharper
anyway: it catches a bias left in place, a correction applied twice and the
swapped numbers, where an expected value computed in Go could only ever be
right on one OS.

`timeout` needed TWO, and only one of them was the one #8374 named:

    set_process_group(pid, pgid)   setpgid(2), both zeroes as written
    proc_waitpid_nohang(pid)       wait4 with WNOHANG, -1 for a live child

`set_process_group` is what the DEFAULT mode is: timeout puts ITSELF in a
fresh group before forking, so the command and everything it starts share one
group and the signal reaches the whole tree. Both zeroes carry the syscall's
own meanings — pid 0 is the caller, pgid 0 is the pid's own value — so
`set_process_group(0, 0)` is the whole call, and `signal_send`'s existing
pass-through of a non-positive pid is the group-directed send that goes with
it. Its failure is ordinary rather than exceptional: a parent and a child
race to make the same call and the loser gets EACCES once the other has
exec'd, which is why both halves make it.

`proc_waitpid_nohang` was not in the issue's list, and it is the one that
decides the shape of the utility. `proc_waitpid(-1)` already reaps whichever
child exits first — the pid reaches wait4 as written — but reports only the
status, so with a command and a deadline timer running there is no way to
tell which of the two woke it. One nohang probe per candidate recovers it,
exactly and without a pid-reuse hazard: a recycled pid is not this process's
child, so ECHILD means "the one I asked about" and nothing else. -1 rather
than 0 for a live child because 0 is what wait4 itself answers there and what
a clean exit decodes to; no errno wait4 returns is 1, so the sign separates
the two.

`nohup` needs one, and it is not a syscall wrapper on a path but a method on
a handle:

    r.dup_onto(fd) / w.dup_onto(fd)    dup3(own_fd, fd, 0)

Redirecting fds 0, 1 and 2 is not a detail of `nohup`, it is the whole job, and
nothing in the language could express it: `stdin()` / `stdout()` / `stderr()`
hand back handles, `open_*` hands back a handle, and a handle surrenders no
descriptor number to pass to a `dup2`. So the operation goes on the handle,
which is where `seek`, `flags` and `isatty` already went for the same reason.

The shape that looks more obvious — an exec that takes redirections — gets the
utility WRONG. Measured against coreutils 9.4: with a terminal on stdin,
`nohup` leaves fd 0 pointing at `/dev/null` opened WRITE-ONLY, so the command
can neither read the terminal nor read the replacement (`cat` there fails with
`Bad file descriptor`, and `echo hi 1>&0` from the same shell succeeds). A
redirection table that typed fd 0 as a Reader cannot say that; `dup_onto` on a
Writer can, because the handle's direction and the destination's number are
independent. The third of nohup's three redirections needs no extra form
either: fd 1 is nameable, so stderr-follows-stdout is `stdout().dup_onto(2)`.

dup2 semantics mean the handle keeps its own descriptor, so the caller closes
it afterwards — the pair GNU makes. The trap worth knowing is when the
handle's own descriptor already IS the destination: dup3 is a no-op and that
close would close the destination. It cannot arise in `nohup`, where the
number being replaced is the terminal and so occupied when the file is opened,
but a caller that closed fd 1 first is not protected by that.

A shell wants the same primitive and needs no new one: fork, redirect in the
child, exec. `timeout` and `stdbuf` were listed as wanting it too and do not —
`timeout` redirects nothing and `stdbuf` sets `LD_PRELOAD`, which is
environment.

`stty` (#8382) needed the one primitive here that does NOT normalise what the
kernel gave it:

    termios_get(fd)              TCGETS, as a 24-element i64[]
    termios_set(fd, when, words) TCSETS / TCSETSW / TCSETSF

`stty -g` prints the four flag words and the control characters in hex and its
restore form reads them back, so the numbers have to be the kernel's — a Fern
bit numbering, which is what `r.flags()` does with F_GETFL, could not
reproduce the output. The flag CONSTANTS are therefore per-OS, and `stty.fern`
branches on `target_os()` exactly as GNU's stty gets them from the C headers.
Measured: the array is `[iflag, oflag, cflag, lflag, line, cc[0..18]]`, which
is the asm-generic struct byte for byte, and GNU prints 32 control characters
because glibc's userspace struct is wider than the kernel's 19 — the top 13
are always zero, which is what the tail of a `-g` line is.

Darwin is the one target it does not reach: its struct is a different shape
and the TIOCGETA request number encodes that struct's size, neither of which
can be written from memory or measured on a Linux build box, so both halves
answer ENOSYS there rather than shipping a number that would answer ENOTTY and
look like "this is not a terminal". `poll` is on the same line for the same
target.

The same utility's `rows N` and `cols N` needed the other half of
`window_size`, which only ever read (#9360):

    set_window_size(fd, rows, cols)   TIOCGWINSZ, then TIOCSWINSZ

Two ioctls rather than one, and GNU makes the same pair: `struct winsize`
carries `ws_xpixel` and `ws_ypixel` beside the two cell counts and GNU
PRESERVES them — measured, a pty planted at 640x480 pixels keeps them across
`stty rows 40`. Since `window_size` surrenders only rows and columns, a caller
could not put back what it never saw, so the read-modify-write belongs inside
the runtime. Both numbers reach the kernel's u16 as given, which is why `stty
rows 65536` reports 0 rows with no diagnostic where `stty rows
99999999999999` is GNU's own `strtoul` range error — two different limits, and
the utility keeps them apart.

`stty -F DEVICE` needed all four of those asked of a HANDLE rather than a
descriptor number (#9363), because the utility opens a path and a Reader
surrenders no number:

    r.window_size()               r.termios_get()
    r.set_window_size(rows, cols) r.termios_set(when, words)

`r.isatty()` is the precedent and the rule: a descriptor question asked of a
handle becomes a method on the handle. Each is a two-instruction stub on the
natives — the fd out of the box, a jump into the helper the free form already
emits — and in the self-host compiler it costs nothing at all, because a
handle IS its fd there and the method lowers to the same op with the receiver
as its first operand. Measured: the open is `O_RDONLY | O_NONBLOCK`, since
`stty -F` on a FIFO with no writer answers `Inappropriate ioctl for device` at
once rather than waiting for a peer.

`buf_push_u64(h, v)` (#9221) is not a syscall wrapper at all — it is eight
bytes into the capacity-carrying builder in one store, little-endian, which is
how every target holds a u64. It exists because a byte at a time is not fast
enough to be worth having: a generator whose arithmetic costs 2.5 ms for 4 MiB
of words spent 23 ms handing the bytes over one `buf_push_byte` call each, and
35 ms through an array append. With it, `std/rand`'s `rng_fill` produces 4 MiB
in 5.6 ms where `random_bytes` needs 15.3, which is what `shred`'s random
passes are built on. Its SSA lowering in the self-hosted compiler is
deliberately absent: that backend stores a string as one byte per EIGHT-byte
word, so the single store would be eight and the op would lose its reason to
exist — a module using it takes the IR path, which is the production default.

`read_dir_all(path)` (#9279) is `read_dir` without the `.` / `..` filter —
every name the directory holds, in the order its reader reports them. It is a
SECOND builtin rather than a change to `read_dir` because dropping those two
is what nearly every caller wants and four walking utilities here rely on;
what wanted the raw order is `ls -f`, which prints the dot entries where the
directory keeps them. Synthesising a pair at the front is right for a sorted
listing and wrong for an unsorted one — `.` is the 18th entry of one tree here
and `..` the 46th — and it also cannot be filtered, where GNU's `-a -I '.*'`
takes both out. On every backend it is the existing emitter with the dot test
suppressed in both getdents passes, which is what makes it cheap; the
interpreter's `read_dir` became the same walk with the filter applied, since
`os.ReadDir` sorts and no backend reproduces that order. The one target that
cannot answer it is wasm32-wasi preview 2, whose `read-directory` omits the
dot entries at the host, so `read_dir_all` there is `read_dir`'s list —
asserted rather than skipped, so a host that started reporting them shows up.

What is deliberately NOT here: `create_dir_all` and `remove_dir_all`, which
already existed. Neither is the primitive `mkdir(1)` or `rmdir(1)` needs —
the first folds every EEXIST into `Ok(())` and cannot say whether it created
anything, the second drains a tree and ignores a missing target — and the
errno they discard is the whole of what those two utilities report.

## Known divergences

**`ptx` breaks a tie between two equal keywords by a pointer, so the corpus
gives each file its own words.** `compare_occurs` falls back to
`_GL_CMP (first->key.start, second->key.start)` when the keywords compare
equal, and ptx reads each operand into its own allocation of `text_buffer` —
so the order of two identical keywords from two different files is the order
the allocator happened to place the buffers in, not a fact about either file.
Measured: `ptx -O nonl sent` over two files both starting `aa bb` prints the
LONGER file's line first whichever order the operands are given in. Within one
file the tie-break is a real offset comparison and is compared in full; the
one case that pairs two files gives them distinct words instead.

**`mv --exchange` is three renames rather than one (#9784).** 9.5 added the
option, and GNU does it in a single `renameat2 (…, RENAME_EXCHANGE)`. Fern's
`rename` has no flag word — the checker's note on it records that a flag one
target honours and two refuse belongs to the capability system — so
`mv.fern` renames the source aside, the destination onto the source, and the
aside name onto the destination, undoing the first when the second fails. The
tree left behind is the same and every corpus case compares equal; what
differs is that a crash between the renames can leave `.mv_exchange.N` behind,
and that a filesystem GNU would refuse for want of `RENAME_EXCHANGE` support
is one three plain renames do not need.

GNU's own failure line on that path is unmatched, and it is a bug rather than
a divergence invented here: `mv.c` sets `x.rename_errno` only when
`n_files == 2 && !x.exchange`, so an `--exchange` that fails reports the `-1`
sentinel — `cannot exchange 'a' and 'nosuch': Unknown error -1`, measured.
Fern names the real errno, and no corpus case pairs `--exchange` with a
failure.

**`pinky --lookup` is accepted and canonicalizes nothing.** 9.5 gave pinky
the option `who` has had for years: the utmp host field run through
`getaddrinfo` with `AI_CANONNAME`. Fern has no name-resolution primitive, so
the option parses and the host prints as the database holds it — which is
also what GNU prints whenever the lookup does not resolve, so every corpus
case compares equal. The option is not implemented until the primitive
exists.

**`uptime`'s `couldn't get boot time` carries an errno nothing on the path
set, so the corpus masks the suffix.** GNU appends `strerror (errno)` to that
message whether or not anything failed: when the database reads fine and
simply holds no boot record, the value left on the thread is the one gnulib's
`proper_name_lite` produced probing the locale at startup — `mbrtoc32` over
`\337\277`, which is EILSEQ under `LC_ALL=C` and succeeds under `C.UTF-8`,
leaving ENOENT from an earlier open instead. Both were measured against 9.12
on one machine. No implementation can reproduce a value that is a fact about
the reference's startup rather than about the run, so the three cases whose
database reads fine and holds no boot record compare the message, the exit
status and stdout, with the suffix masked off both sides (`stderrMask`).
Where the open itself FAILED the errno is that failure's, and those cases
compare it in full.

**`nohup` has one diagnostic GNU has no counterpart for**: a dup3 onto fds 0,
1 or 2 that fails reports `failed to redirect standard input` / `output` /
`error` and exits 125. Nothing can measure what GNU says there, because the
call has no reachable errno — the source descriptor was opened a line earlier
and the destination is one this process holds open — so rather than guess at
GNU's wording the line is plainly ours, on a path no corpus case reaches. The
statuses around it are measured: 125 is what nohup's own `--help` documents
for a failure of nohup itself.

**`stty`'s `--help` disagrees with `stty` in two places, and the BEHAVIOUR is
what the port follows.** `cooked` is documented as putting "eof and eol
characters to their default values" and does not: `stty eof Z; stty cooked`
leaves eof at Z, measured. `raw` is documented without `-iutf8` and clears
it: `stty iutf8; stty raw` leaves iutf8 off. Both are reproduced as measured,
and the help text is reproduced as GNU prints it, so the port carries the
same disagreement rather than a third answer.

**`stty`'s two failure messages come from two different layers, and the
second one is glibc's.** A pseudo-terminal silently ignores CSIZE, PARENB and
a cleared CREAD: the raw TCSETSW returns 0 with nothing changed, straced.
glibc's `tcsetattr` notices and synthesises EINVAL, which is
`stty: 'standard input': Invalid argument`; when it is satisfied, `stty`'s own
read-back comparison reports `unable to perform all requested operations`
instead. Which one appears is decided by a rule measured over the whole
cflag, and reproduced in `stty.fern` because there is no other way to match
the output:

- none of the four flag words or the line discipline moved, and the request
  wanted one of them to → `Invalid argument`
- something moved, but not all of it → `unable to perform`

The control characters are NOT in glibc's comparison (`stty cs7 eol ^M` still
reports the refusal), a requested CSIZE of CS5 is excluded because its bit
pattern is zero and cannot be told from "unchanged" (`stty cs5` reports the
other message), and an input speed of zero can never satisfy it at all —
glibc records "input follows output" in a private flag in the top bit of
c_iflag that the kernel never stores, so `stty 0` and `stty ispeed 0` always
report the incomplete set while `stty ospeed 0` succeeds.

**`stty`'s two passes are visible, and one warning proves it.** The names of
every operand and the VALUES of all but `rows` and `cols` are read before
anything is applied, so `stty rows 40 line abc` changes nothing while
`stty rows 40 cols -1` leaves 40 rows behind. `line N` warns for an N past
255 without failing — and the warning is printed TWICE, once per pass, which
is the clearest evidence of the arrangement and is reproduced.

**What a `shred` corpus can compare, and what nothing can.** GNU's pass
SCHEDULE is drawn at random: two runs of `-n 10` over identical files disagree
on both the order of the patterns and the SET of them, so `-v` output past the
all-random range compares against nothing, GNU included. Every case in the
corpus therefore stays at `-n 3` or below, where every pass is `random`, or at
`-n 0`, where there are none. The BYTES are unrepeatable for the same reason
except through `--random-source`, and there the whole content compares — which
is what gates the write path, including the sweep GNU runs over a file about
to grow: a file shorter than one block is overwritten at its own length first,
once per pass and silently, and only then at the rounded-up length where the
pass lines are, so an 11-byte file under `-n 2` is written 11, 11, 4096, 4096.
That sweep is invisible to a source of one repeated byte, so the corpus seeds
a second source whose every byte differs.

`-` is comparable too, and it is the one operand whose answer depends on how
fd 1 was OPENED rather than on anything in the file: a pipe is `invalid file
type`, an append-only descriptor is `cannot shred append-only file
descriptor`, a closed one is `fcntl failed: Bad file descriptor`, and a plain
writable file is overwritten in place — `-u` then truncating what it cannot
unlink. `w.flags()` (#9219) is what tells those apart. The harness reaches all
three through `stdoutPath` (an append-only fd 1) and `stdoutFile` (a writable
one, whose whole content is what the case compares).

**What an operand IS decides before any of that.** GNU refuses a FIFO, a
socket and a TERMINAL with `<name>: invalid file type` and exit 1 — none has a
length to rewind over — while a character or block DEVICE is overwritten like
a file, which is what shred was written for. Measured, GNU 9.4: a FIFO with a
reader held on it gives the refusal, a unix socket never reaches the question
because its open answers ENXIO (`failed to open for writing: No such device or
address`), a terminal gives the refusal whether it arrives as a name
(`/dev/ptmx`, `/dev/pts/0`) or as `-` on a terminal fd 1, and `/dev/null` is
written and exits 0 even though `fdatasync` on it answers EINVAL — GNU asks
for a full `fsync`, is refused the same way, and carries on. The corpus gates
all of it; the terminal is `w.isatty()` (#9229), which is the question a MODE
cannot answer, since /dev/null and /dev/ptmx are both character devices and
only one of them is written.

`-f` is an open RETRY rather than an eager chmod, and gating it matters to the
TREE rather than to the output: GNU makes the entry writable only after an
open refused for the one reason a mode can fix, so `shred -f <dir>` reports
`Is a directory` having changed nothing. Retrying on any error instead leaves
the directory at 0200 on a run GNU performs cleanly.

**A device's length is not in its stat**, and that is the whole of `shred`'s
classic use. `st_size` is 0 for a block device, so GNU asks
`lseek(0, SEEK_END)` and writes what that reports; when even that answers 0 —
every character device — the length is unknown and it writes until a write
FAILS, which on /dev/null never happens. Both are what `shred.fern` does.
Measured by hand, since the harness cannot make a device and a case that does
not terminate is not a case:

```
$ head -c 262144 /dev/urandom > img && losetup --find --show img
/dev/loop0
$ shred -n0 -z /dev/loop0 && tr -d '\0' < img | wc -c
0                       # the whole 256 KiB, and fern's run leaves the same
$ shred -n1 -v /dev/loop0        # one `pass 1/1 (random)...`, exit 0
$ shred -n1 -v /dev/null         # writes forever; only `-s` makes it stop
```

The write ERROR in that family matches too, offset included:
`<name>: error writing at offset 262144: No space left on device`. The offset
is the byte the failing write stopped at, which for ENOSPC lands INSIDE a
block, so counting the blocks handed to `w.write` could not produce it — the
pass loop goes through `w.write_some` (#9231), one write and the count it
returned, and adds that count before it builds the message. Measured both
ways, byte for byte against GNU: a 256 KiB loop device asked for 400,000
bytes, and a 256k tmpfs asked the same. Neither is a corpus case, because the
harness mounts nothing.

**What a `dd` corpus can compare, and what nothing can.** The third report
line carries the elapsed time and a rate computed from it, so two correct
implementations can never agree on it. A mask cannot reach it either — the
harness masks stdout and the tree, never stderr, and the whole report is on
stderr — so every case in the corpus names `status=noxfer` or `status=none`
and the third line's SHAPE is pinned separately, on our own output
(`TestDDTransferLine`). Everything else is comparable and it is most of dd:
the two record-count lines, every byte of the output file, the operand
grammar with its five diagnostics, and what each `conv=` does to the bytes.

`status=progress` is the one place the durations are worth masking rather
than avoiding. It writes one `\r`-prefixed transfer line per second, and
the seconds THERE are a whole number: an elapsed 1.7 s reads `2 s`, so it
rounds rather than truncates. A record that outlasts several seconds still
produces one line, not one per second, because GNU's alarm handler only
raises a flag that the copy loop reads at a record boundary — which is why
this needs no signal handler to match, and `dd.fern` takes the same look at
the same place. `TestDDProgress` drives both sides over a FIFO fed one byte
and then another after a sleep, so the bytes at the tick are identical, and
compares stderr with only the fractional durations rewritten.

**dd's number grammar is not gnulib's.** On top of the block suffixes every
utility here shares it adds `c` (1) and `w` (2), an `x` between two numbers
that MULTIPLIES (`1kx2` is 2048, `2x3x4` is 24), and a trailing `B` that
makes a count BYTES rather than blocks. Measured, GNU 9.4, because none of
the four is stated precisely in the man page:

- `c` and `w` take no `iB` form and no second multiplier, so `1ciB` and
  `1kc` are refused while a bare `c` is 1 and `3w` is 6;
- the `B` is read off a piece's END, which means the multiplier grammar's
  own endings carry it — `count=1kB` is 1000 BYTES where `count=1k` is 1024
  blocks, and `1KiB` is 1024 bytes — and a `B` left over after a number or
  a block suffix is dropped, so `2B` is 2 and `1bB` is 512. One `B`
  anywhere in the product marks the whole operand, so `1cx7Bx2` is 14
  bytes. It reaches the same bit as `iflag=count_bytes` and its two
  siblings;
- a piece that is a single `0` in front of an `x` is WARNED about rather
  than refused (`warning: '0x' is a zero multiplier; use '00x' if that is
  intended`), once per piece, and the zero still wins — which for a block
  size is then the invalid `bs=0`;
- a value past INTMAX_MAX is a different line from a value that is not a
  number: `invalid number: '1x': Value too large for defined data type`
  against a bare `invalid number: 'q'`.

`iseek` and `oseek` are aliases for `skip` and `seek`, so the pair is one
operand for the last-wins rule: `skip=1 iseek=3` skips 3 and the reverse
skips 1.

`conv=fdatasync` on a descriptor with no data-only sync reports
`fsync failed`, not `fdatasync failed`: EINVAL there is the descriptor
saying it has no such call rather than a failure, so GNU promotes the
request to a full `fsync` and reports THAT one's errno. /dev/null is where
it shows.

**What dd here does not do yet**, each an accepted-operand gap rather than a
wrong answer — the name is refused as `invalid conversion` / `invalid
input flag`, which is itself the divergence:

- `conv=ascii`, `conv=ebcdic` and `conv=ibm` need the two EBCDIC tables
  (#9240);
- `conv=sparse` needs a write that punches a hole rather than writing NULs,
  which is `w.seek` past the gap — the primitive is here, the accounting is
  not (#9241);
- `iflag`/`oflag` `direct`, `directory`, `dsync`, `sync`, `noatime`,
  `nocache`, `noctty` and `nofollow` are all open-time bits, and Fern's
  `open_reader_with` / `open_writer_with` flags word carries two: create
  and non-blocking (#9242);
- the SIGUSR1 report mid-copy needs a signal a program can OBSERVE, and
  Fern has only the three disposition calls (`signal_send`,
  `signal_ignore`, `signal_default`) — nothing that runs or records on
  delivery (#9243);
- `conv=excl` that CREATES its output leaves mode 0600 where GNU leaves
  0666 through the umask, since Fern's exclusive open fixes the mode;
  #9237 is the flags-word bit that closes it, and it is why the corpus
  holds only the taken-name half of that case.

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

**`mknod`'s `invalid device MAJOR MINOR` is unreachable.** GNU composes the
two numbers with `makedev` and refuses the pair when the result is `NODEV`.
On Linux `dev_t` is 64 bits wide and no pair of 32-bit numbers can reach
`(dev_t) -1`, so the message is dead code there — on Darwin, where `dev_t` is
`int32_t` and the packing is `major << 24 | minor`, `mknod n c 255 16777215`
does print it. Fern's `mknod` takes the major and the minor as a PAIR and each
backend packs the word its kernel wants, precisely so that a program never
composes a target-specific number; there is therefore nothing here that can
answer `NODEV`, and reproducing the message would mean writing XNU's dev_t
layout back into the utility. Every other refusal of a device number — the
base-zero parse and the 32-bit range check — is byte-exact, and the corpus
compares them.

**`mkdir -Z` and `mkdir --context[=CTX]` on a kernel that HAS SELinux.** GNU
sets a security context and no primitive here can, so the option is refused —
`setting a security context is not supported on this system`, exit 1 — rather
than quietly ignored, which is the answer `runcon` gives for running a command
in a context. No machine the corpus runs on has SELinux, so the compared path is
the one where GNU warns (`--context=CTX`, once per occurrence) or says nothing
(`-Z`, a bare `--context`) and carries on.

**`chcon` refuses the context change itself, and there is no answer that
would not.** A security context is an extended attribute; there is no
`getxattr` (#9098) or `setxattr` (#9154), so the one step it exists for
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

**`date -s` cannot set the clock.** GNU parses the string and calls
settime; Fern has no builtin that writes the system clock, so `date -s
STRING` and the `date MMDDhhmm[[CC]YY][.ss]` operand form parse their
argument exactly as GNU does — an invalid one is refused with the same
message — and then print the date after `cannot set date: Operation not
permitted`, which is what GNU prints without the privilege. The corpus
holds only invalid spellings of both, since a valid one run by the
root the suite runs as would move the machine's clock.

**`df --sync` does not sync.** GNU calls `sync(2)` before it measures, so its
numbers are post-writeback. Fern has no such builtin — no `sync`, `fsync` or
`syncfs` in FuncSigs, and no flush-to-device on `Writer` — so `df.fern` accepts
the option and does nothing with it, which is what `--no-sync` does. The corpus
cannot see it: the only filesystems whose numbers are stable enough to diff
between the GNU leg and the Fern leg are the ones reporting no blocks at all,
and flushing changes nothing there. #9089 carries the four sync calls and the
classifications they need; #9102 was filed separately for this caller and is the
same builtin.

**`uptime` is Linux-only, and says so.** The three load averages are
`/proc/loadavg`, which is what glibc's `getloadavg(3)` reads; Darwin answers
the same question through `sysctl(KERN_BOOTTIME)` and `getloadavg(3)` with no
file behind either and no primitive for it. A build for another target
therefore REFUSES after the option scan — `the load averages are read from
/proc/loadavg, which darwin does not have`, exit 1 — rather than printing the
line without its load clause. That shape is one GNU also produces, when
`getloadavg` fails, so printing it would look like an answer instead of a gap.
`--help` and `--version` still answer everywhere, because the refusal comes
after the scan.

Two things about the reference are worth writing down, because both cost a
round of wrong work. GNU coreutils' `uptime` has **no `-p` and no `-s`** —
those are procps', as is the `/usr/bin/uptime` on most Linux distributions —
and **where it takes its boot time changed in 9.4**: 9.1 reads `/proc/uptime`
and lets it OVERRIDE the utmp `BOOT_TIME` record, so a fixture database proves
nothing against it, while 9.4 and 9.10 take the record. The oracle is pinned
to 9.4 for that reason.

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

`QUOTING_STYLE` reaches `%N` and nothing else, and only when the format as
written holds the two bytes `%N`: `%-N`, an octal-escaped `%` and the default
block never read it, `%%N` does. The ten gnulib styles live in `lib/gnu.fern`
as `quote_style`, with `quoting_style_from_env` doing the ARGMATCH lookup and
the `ignoring invalid value` warning, so `ls` can pick them up as is.

**`timeout` cannot forward a signal that arrives at IT.** GNU handles SIGINT,
SIGQUIT, SIGHUP, SIGTERM and SIGXCPU by passing them on to the command and
then dying of them, so `kill -INT` on a `timeout` reaches the command it is
supervising. That needs a signal a program can OBSERVE rather than only
dispose of, which is #9243 — the same gap `dd`'s SIGUSR1 report waits on.
Everything else about the utility is here, and nothing in the corpus reaches
this: it needs a third party signalling the timeout process mid-run.

One smaller residue comes from `proc_waitpid` collapsing a signal death into
the shell's one number. A command KILLED reports 137 after a timeout where a
command that merely timed out reports 124, and GNU tells the two apart with
WTERMSIG; here the SIGNAL THIS PROCESS SENT stands in for the half the status
cannot carry, so every case timeout itself can produce is right — `-s 0` over
a command that exits 137 is 124, as GNU's is — and a SIGKILL arriving from
somewhere ELSE during the timeout reads as 124 here and 137 there.

**`timeout`'s deadline is a forked child, and that is visible to nothing but
`ps`.** GNU arms `alarm(2)` and lets the handler interrupt its `wait`; with no
observable signal (above) the clock is a process instead, and ONE blocking
`proc_waitpid(-1)` wakes on whichever finishes first — the command or the
timer. `proc_waitpid_nohang` then says which, because a child already reaped
answers ECHILD where a live one answers -1. The timer holds no descriptor the
command can see and is reaped before timeout exits; while the command runs
there is one extra process in the group, which no comparison of stdout,
stderr, status or the tree can reach. It costs one fork and one reap per
invocation — measured at two tenths of a millisecond in the table above,
against a startup lead that more than covers it.

**`du` cannot walk past `PATH_MAX`.** Every filesystem primitive takes a PATH,
so a component 8 KiB down is `File name too long` where GNU's fts, which opens
each directory and reads it fd-relative, keeps going. Measured on a 40-level
tree of 200-byte components: GNU 168, Fern 80 plus one `cannot access` per
level it could not reach, exit 1 — the same shape an unreadable directory has,
so it degrades rather than lying. #9078 and #9074 are the same `openat` /
`fdopendir` / `fstatat` family, which `rm -r`, `ls -R`, `find` and `cp -r` all
want too.

**`dircolors -p` prints GNU 9.12's database.** The text `-p` prints is a data
file that changes between coreutils releases — the copyright year on its third
line moves, and entries come and go — so there is no version-independent answer
to print. `coreutils/lib/colordb.fern` carries GNU 9.12's, transcribed from that
binary's own `-p` output (the file grants permission to copy and distribute it
with its notice preserved, which is why it can be carried at all). `-p` and
every invocation with no FILE read that text, so against a different oracle
those cases fail loudly rather than passing something wrong, as `uname -p` does
on a distribution binary; the fix is to transcribe that release's `dircolors
-p`. Nothing else in the utility is version-sensitive.

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

**`pr -D` with `%D`, `%F`, `%R` or `%T` prints uninitialised memory in
GNU 9.12.** `init_header` sizes the date buffer by calling gnulib's
`nstrftime` with a null buffer first, and for the four directives that
nstrftime expands into a sub-format that counting pass gets the length
wrong: the header then carries whatever was in the allocation. Two runs
of the same command print different bytes, so there is nothing for a
corpus to compare — `pr_test.go` uses `%m/%d/%y`, `%Y-%m-%d`, `%H:%M`
and `%H:%M:%S` in their place, which are the same dates by a route
nstrftime measures correctly. `pr.fern` expands all four properly;
against a fixed 9.12 it will agree, and against this one it differs on
purpose.

**`uname -p` and `-i` print `unknown` on Linux, and `-a` omits both.**
Coreutils can answer neither there — the two `#if`s in uname.c are a
Solaris `sysinfo(2)` and a BSD `sysctl`, and glibc has neither — so an
upstream build says `unknown` for each and the `-a` omission rule drops
them. That is what `uname.fern` does, because the oracle every lane
compares against is a build from the GNU tarball rather than a
distribution's binary. Distributions patch `-p` and `-i` to the machine
name (Debian, Ubuntu, Fedora and RHEL all ship that patch, and under it
`setarch linux32 uname -p` follows `-m` to `i686`), so running the corpus
against a distribution `/usr/bin/uname` instead fails loudly on the `-p`
/ `-i` / `-a` cases rather than passing something wrong. On Darwin `-p`
is the CPU family (`arm`, not `arm64`) and `-i` is still unknown.

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

It is also why `du` loses every bench row it has (0.29x to 0.73x). `du.fern`
makes FEWER syscalls than GNU on the same tree (1505 against 2064 on a
20-file directory 60 levels down: no fcntl, fstat or fdopendir), but each of
its 1260 `newfstatat` calls carries the whole path, so the kernel walks every
component again — 9 µs a call against GNU's 4 with a directory descriptor
and a bare name. A `chdir` walk would recover the time and is not taken: it
is the fts mode GNU abandoned, and it changes which path an error names.

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

`ls -R` is the fifth, and there the bound is only depth: it rebuilds the
path per entry the way the other four do, and a tree deeper than PATH_MAX
stops with `File name too long` where GNU keeps walking.

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
corpus was held to "9.4 or newer" when these were written, and a case could
only assert what every version in that range did, so the two are implemented
to 9.10 and left uncompared. Now that the corpus names one version they could
be compared instead; nothing has needed it yet. `chown` is unaffected
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

**A line RECORD no longer costs a copy per line (#8815).** `join` holds TWO
input cursors at once and carries a parsed line from one loop iteration to
the next, and it was 0.10x GNU on every workload while the line was cut out
of the read block as a string, split into a field array, and threaded through
a five-tuple with the writer and the run state. It is 0.33x now (408 ms
against GNU's 133 on two 1M-line files, 1.98G instructions against 5.0G on a
300k-line slice): the line is a byte range of the block it was read from,
with the join field, the field count and the first four fields' bounds in
the record; the cursor carries the current line and comes back alone; the
key comparison is one `__mismatch`; and output is pushed straight into the
writer's builder. The floor that is left is the backend's, not the shape's:
an indexed byte loop is ~40 retired instructions a byte (a tagged-pointer
check, a bounds check and a push/pop boolean chain per compare), and a line
still costs two allocations (its record and the cursor rebuild).

**Signal disposition control (#8792).** `tee` is blocked on it and is not
written yet. `-i` is `signal (SIGINT, SIG_IGN)`, and the whole `-p` /
`--output-error` family turns on whether SIGPIPE is ignored: GNU leaves it
at its default so `tee` DIES of SIGPIPE, and the moment either option is
given it ignores it so the write returns EPIPE and the mode picks one of
four behaviours. Four of those five rows are unreachable without the
primitive, and a `tee` that accepted the options and did nothing would be
exactly the carve-out this document forbids.

**A byte-range comparison no longer costs a copy (#8791).** `__mismatch(a,
ao, b, bo, n)` is the comparison kernel: it answers the first differing
offset of two ranges without slicing either, at memcmp speed. `uniq`, `comm`,
`join` and `sort` compare through it; `comm` went from 0.26x to 0.77x GNU on
it and `uniq` to parity in retired instructions, and what separates those
two from GNU now is the per-line bookkeeping around the compare, not the
compare.

Gaps that are closed, each now exercised by the corpus rather than carved
out of it: `IoError.Other` carrying no strerror text (#8265), in the
write-failure cases (`yes >&-`, `> /dev/full`); source unable to learn its
compile target (#8338), in `yes.fern`'s per-target block; and fstat/lseek on
a DESCRIPTOR (#8713), which `cat` needs to refuse a closed fd 1 before it
reads anything and `tail` needs to read a regular file from its end — landed
as `r.stat()` / `w.stat()` / `r.seek()` on every backend, `w.seek()`
beside them for the utilities that write at an offset, and `r.flags()` /
`w.flags()` (#9219) for the one question a descriptor answers and a path
cannot: what the handle was OPENED for — 1 readable, 2 writable, 4
appending — which is how `shred -` tells an append-only standard output,
which it must refuse, from a writable one, which it overwrites. `isatty` was
the same shape one step further on: it took a descriptor NUMBER, so only the
three stdio fds could be asked, and `shred` has to refuse a TERMINAL operand
it opened by name — `r.isatty()` / `w.isatty()` (#9229) ask it of the handle. `hostid` wanted a
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
  `mkdir` `rmdir` `rm` (done) `mv` `cp` (cp done — the recursive copy
  engine is `lib/copy.fern`, shared by design: GNU keeps one `copy()`
  behind cp, mv's cross-device fallback and install, and so do we. It
  needed NO new primitive — chmod, chown_at, set_file_times,
  create_symlink, create_link, read_link, mknod, `Writer.seek` and
  stat/lstat's full field set were all already here, so #8352's
  "blocked on openat/fchmod/fchown/utimensat/copy_file_range/lseek
  SEEK_HOLE, symlink" was stale but for the clone. `--reflink=always`
  reports the failure GNU reports where the filesystem cannot clone,
  which is what ext4 and overlayfs answer and not what btrfs does:
  FICLONE is the one primitive still missing. Holes are punched at
  st_blksize granularity, which reproduces GNU's SEEK_HOLE result
  without it. A recursive copy walks a directory's entries in
  ascending INODE order — measured, and neither readdir order nor the
  names sorted nor directories first — so the engine stats each entry
  for the number readdir already had, which #9317 would give it back.
  The destination is created BEFORE the entry list is read, because a
  destination inside the source is then met as one of the walk's own
  entries: recognising it there is both what stops `cp -r d d/sub`
  recursing without end and where GNU's into-itself diagnostic comes
  from, which is why that line arrives after the operand's copy rather
  than before it. A directory's `-v` line belongs to its CREATION, so
  an existing destination directory gets none.
  mv's EXDEV fallback is DONE and is this engine: a `rename` that comes
  back EXDEV copies with `-a`'s attributes and then removes the source,
  which is what `--no-copy` asks it not to do. Three things separate it
  from a plain `cp -a`, each measured. The destination is REMOVED before
  the copy, because a move replaces where a copy merges — a destination
  directory that will not go reports `inter-device move failed: 'src' to
  'dest'; unable to remove target: Directory not empty` and copies
  nothing, and that removal is also why `created directory` always
  prints: the destination is always freshly made. The source is removed
  only if the WHOLE operand copied, so a copy that failed part way leaves
  even the files that did copy in place, while a later command-line
  operand still runs. And the two halves walk in DIFFERENT orders — the
  copy by inode, the removal in readdir order, which is `rm -rv`'s walk
  and byte-identical to it.
  The wording is a verbose STYLE on the engine rather than two engines:
  `cp` prints `'src' -> 'dest'` for everything, `mv` prints `copied 'src'
  -> 'dest'` and `created directory 'dest'`, and only `mv` suppresses the
  line for a `--preserve=links` follower, whose name is linked rather than
  copied. The removal half is mv's own rather than `rm`'s walk with the
  policy stripped out: `rm`'s is inseparable from its prompting, its
  --preserve-root and -I refusals and its tri-state result, none of which
  a move consults)
  `install` (done — the engine's THIRD caller, after cp and mv's EXDEV
  fallback, and it needed no new primitive: chmod, chown_at,
  set_file_times and proc_fork/proc_exec/proc_waitpid were all already
  here. THE UMASK IS NOT CONSULTED, which is the thing cp would mislead
  you about: the default mode is 0755 under `umask 077` and `-m
  u=rw,g=r` is 0640, so `copy.Cfg.forced_mode` sets the mode outright
  rather than filtering anything. `-m`'s symbolic form resolves against a
  base of ZERO and not against that 0755 — `-m +x` is 0111, `-m go-w` is
  0, `-m o=` is 0 — so 0755 is what install uses when `-m` is ABSENT and
  is not something `-m` edits; even the bare `+w`, which POSIX lets
  consult the umask, is 0222 under every mask. `-m` with `-d` reaches
  only the LAST component. A directory's line is `install: creating
  directory 'X'`, on stdout and carrying the program prefix where the
  file line has none, and the final component is named as the operand was
  written, trailing slash included. An existing destination is always
  REMOVED first, so `removed 'X'` precedes the copy line unless `-b`
  renamed it away. `-C` on a match does nothing at all, mtime included.
  A failed `-s` prints the `-v` line TWICE and leaves no destination:
  the `cannot run` message comes from the forked CHILD, and both
  processes carry the same unflushed stdout across the fork. `--debug`
  prints cp's own second line, so install widens the FOURTH exemption
  rather than adding a fifth. `install: cannot change permissions of 'X'`
  is the one quirk the corpus does not reach, for the reason `cp -f`'s
  retry does not: as root it needs a destination whose mode cannot be
  set. Faster than GNU on 11 of its 12 bench rows and at parity on the
  64 MiB throughput row, which is #9309's copy_file_range gap and not
  install's)
  `touch` (done — the open that creates the file is `open_writer_with` under the create and non-blocking bits, GNU's `O_WRONLY|O_CREAT|O_NONBLOCK`, so a FIFO or socket operand opens or fails exactly as GNU's does; `-` reaches standard output as /proc/self/fd/1 — see `touch.fern`'s header) `truncate` (#9142) `mkfifo` (done) `mknod` (done)
  `sync` (done, on `sync()`, the fsync / fdatasync / syncfs handle
  methods from #9181 and the non-blocking `open_reader_with` /
  `open_writer_with` from #9197, which is what opens a FIFO with no
  peer as GNU does) (rename,
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
  those: `env()` for $SHELL / $TERM / $COLORTERM and no new primitive), `date` (done — the grammar behind `-d`, `-f` and `touch -d` is `lib/datetime.fern`, a port of gnulib's parse_datetime with its mktime emulation and the `--debug` trace, over `lib/tz.fern`; the `-s` and `MMDDhhmm` forms parse as GNU does and then report `cannot set date`, because no builtin sets the system clock — see the divergence below), `nice` (done, on the
  new `priority()` / `set_priority(n)` pair under the `sched` target
  capability — and on `gnu.exec_command`, which `env` moved its own
  execvp emulation into so the PATH search and the `/bin/sh` retry for a
  shebang-less script are written once, #9262), `timeout` (done, on
  `set_process_group` and `proc_waitpid_nohang` — see the primitives
  section above and the two divergences; the default mode's process group
  is the reason it waited, and the deadline being a forked child rather
  than an alarm is the reason the second primitive exists)
  `nohup` (done, on `dup_onto` — the handle that installs itself at a
  descriptor, which is what all three of its redirections are; the message
  wording it emits is 9.4's, see the divergence below) `kill` (done, on the
  shared `operand2sig` in `lib/signals.fern` — GNU's own signal-operand
  grammar, which `env` and `timeout` had each half-implemented differently,
  #9652) `chroot` (done — the last utility in the catalogue, on the four
  primitives of #9678: `chroot`, `setgroups`, `setgid`, `setuid`. Everything
  above them was already here, and the two lookups it runs — once outside the
  new root and once inside — are the reference's shape rather than
  belt-and-braces), `stdbuf` (**not planned**, #8378: it needs Fern to call
  host libc, both `setvbuf` and libc's `stdout` / `stdin` / `stderr` DATA
  symbols, and Fern has no FFI. Syscalls cannot substitute, because buffering
  is a libc userspace concept with nothing behind it in the kernel. That is
  why the catalogue is 106 of 107), `dd`
  (done, on `w.seek(offset, whence)` — lseek on a Writer, which is what
  writing at an offset without rewriting the file needs; the operand
  families it does NOT have are in the divergences above, each with its
  own issue, and the biggest of them wants a signal a program can
  OBSERVE rather than only dispose of, #9243)
  `shred` (done, on that same seek — see the divergences above) `stty` (done, on
  the kernel's own termios words plus `set_window_size` and the four handle
  forms — #9356, #9360, #9363; the two places its `--help` text disagrees with
  its own behaviour, and the glibc verdict layer its two failure messages come
  from, are in the divergences below), `uptime` (done — no new primitive: the boot time and the
  session count are the utmp database `read_file_bytes` already reads, the
  clock is `lib/tz.fern` plus `lib/timefmt.fern`, and the load averages are
  `read_file` of /proc/loadavg), `pathchk` (done — it needed no new primitive:
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
  (271), `rlimit_nofile` (272), `truncate` (273), `mknod` (274),
  `priority` (282), `set_priority` (283).

  `truncate` is path-based rather than fd-based because no Fern open
  yields a writable descriptor to an EXISTING file without first
  emptying it: `open_writer` is `O_WRONLY|O_CREAT|O_TRUNC`,
  `open_appender` creates too, and `open_exclusive` fails on a file that
  exists. It does not create: that is `open_exclusive` followed by this.
  The shape costs a divergence GNU does not have (#9142): GNU opens once
  and calls `ftruncate(2)`, so it resizes a file whose MODE would refuse
  a fresh open, and `umask 222; truncate -s 5 new` succeeds there and
  fails here with `Permission denied`. `truncate(1)` therefore waits on
  the descriptor form; the handle methods `dd` and `shred` want —
  `w.truncate(len)` and `w.seek(offset, whence)` — are both here.

  Neither WASI preview has a path-based set-size —
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
  `process_alive`, `rlimit_nofile` and the `priority` / `set_priority`
  pair are refused there too, named by
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
