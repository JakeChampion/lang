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
| `csplit` | done — line-number, `/REGEXP/[OFFSET]`, `%REGEXP%[OFFSET]` and `{N}` / `{*}` patterns with the two cursors GNU leaves behind, `-b` `-f` `-k` `-n` `-s` `-z` `--suppress-matched`, the printf suffix format and its flag rules, and the four failure paths that each leave a different piece behind |
| `od` | done — `-A -j -N -S -t -v -w --endian --traditional` and the ten hidden format letters, the column arithmetic that lines several `-t` specs up with each other, the `z` printable trailer, duplicate-block folding, the traditional offset and label operands, and gnulib's shortest-round-trip float rendering at the target's `float`, `double` and `long double` |
| `nl` | done — `-b` `-h` `-f` with the `a` / `t` / `n` / `pBRE` styles, `-d` in all three delimiter forms, `-i` `-l` `-n` `-p` `-s` `-v` `-w`, and the line-number overflow reported one line after it happens |
| `ptx` | done — the column, roff and tex formats, the sentence and word alphabets and their regexps (glibc syntax 0, so `lib/bre.fern`'s `emacs_syntax()`), `-b` `-i` `-o` `-f`, `-A` / `-r` / `-R` with a reference per OCCURRENCE rather than per context, `-F`'s escape table (octal wants its leading zero, `\c` truncates, `\e` is not an escape), and `-G`'s output operand, which is created and truncated before every check but the option-parse-time ones. The column arithmetic is measured, not derived: `-G` is not the same layout one column narrower, an empty `before` prints its flag AT the half-line column and pushes the rest right, and the head's share is the widest keyword in the whole input. 11-16x slower than GNU, and the cost is per occurrence rather than per byte (#9083) |
| `join` | done — `-1` `-2` `-j` `-a` `-v` `-e` `-o` `-t` `-i` `--check-order` `--nocheck-order` `--header` `-z`, the obsolete `-j1` / `-j2` and multi-argument `-o` forms with the counting that tells an option argument from a file, and GNU's order check with its default weakness |
| `tr` | done — the whole SET grammar (ranges, `[:class:]`, `[=c=]`, `[c*n]`, `[c*]`), `-c` `-C` `-d` `-s` `-t`, the case-fold pairing of `[:upper:]` and `[:lower:]`, and the lazy SET2 extension that decides which of two faults a mismatched pair is reported as |
| `split` | done — `-a` `-b` `-C` `-d` `-e` `-l` `-n` `-t` `-u` `-x` `--additional-suffix` `--numeric-suffixes[=FROM]` `--hex-suffixes[=FROM]` `--verbose`, the widening suffix counter (`xyz` then `xzaaa`, so 650 two-letter names, not 676), and all six `-n` forms. Chunking a non-seekable input spools it to `$TMPDIR/cutmpXXXXXX` through `open_exclusive` (#8776) as GNU does. `--filter` needs a child whose stdin is a pipe the parent writes (#8810) and is declared but refused |
| `sort` | done — `-b` `-c` `-C` `-d` `-f` `-g` `-h` `-i` `-k` `-m` `-M` `-n` `-o` `-r` `-R` `-s` `-S` `-t` `-T` `-u` `-V` `-z`, `--debug`, `--files0-from`, `--sort=WORD`, `--check=WORD`, `--random-source`, the obsolete `+POS -POS` key form under all three `_POSIX2_VERSION` regimes, and the hidden `-y`. The whole input is read before anything is written, so `sort -o f f` works, and the lines are offsets into one buffer that a stable merge permutes. `--batch-size` above the process's open-file limit needs `getrlimit` (#8819) |
| `shuf` | done — `-e` `-i LO-HI` `-n` `-o` `-r` `-z` `--random-source`, and the generator they all run on, reproduced draw for draw: one (num, max) pair of 64-bit words that divides its leftover entropy down instead of refilling (ten numbers out of three bytes), the rejection that keeps a draw unbiased, and the WRAPPING refill a range past 2^57 reaches. Which of the two sampling paths runs is fstat(2)'s answer as it is GNU's — an input past 8 MiB with `-n` and without `-r` is a reservoir, one draw per line plus one for the read that ends it, and a pipe always is — because the two consume different draws. `-n` takes the minimum of its occurrences and drops an overflow; `-o` opens after the draws, so an exhausted source leaves the file intact while `-n 0` and a `-r` with nothing to repeat have already emptied it. A range too large to materialise is permuted through a sparse map. Startup beats GNU; every per-line row loses by 1.4-5x |
| `tee` | done — `-a` `-i` `-p` `--output-error[=MODE]`, the FILEs opened before the first read and each read block written unbuffered to every output. The mode family is a signal disposition first: without it SIGPIPE keeps its default and tee dies of it, and `-i` / `-p` are SIG_IGN on SIGINT / SIGPIPE (`std/signal`). |
| `hostid` | done — glibc's gethostid: `/etc/hostid`, else the hostname's IPv4 address through NSS (`lib/resolv.fern`: nsswitch's `hosts:` line, `/etc/hosts`, `/etc/resolv.conf`, an RFC 1035 A query over TCP) with its halves swapped, else 0; `extra operand` for anything. Needs `hostname()`, so it is a native-target utility: WASI has no host identity |
| `whoami` | done — the effective uid's name from `/etc/passwd`, `cannot find name for user ID N` when it has none, and every operand (a lone `-` included) an `extra operand`. Needs `geteuid()`, so it is a native-target utility |
| `id` | done — the composite line with `euid=` / `egid=` when they differ, `-u` `-g` `-G` `-n` `-r` `-z` `-a`, `-Z` refused on a kernel with no SELinux, and USER operands looked up by name and then as a decimal uid. Needs `getuid()` / `getgid()` / `getgroups()` |
| `groups` | done — the process's own group names or one `USER : g1 g2` line per operand, with a failed operand costing the status and not the run, and an unnamed gid diagnosed and printed as its number. Needs `getgroups()` |
| `logname` | done — getlogin(3) in glibc's two steps: `/proc/self/loginuid` and then the utmp user-process entry for the controlling terminal, whose name is `lib/tty.fern`'s ttyname(0) without the `/dev/` prefix ut_line carries. No login is `no login name` and exit 1, never the euid's name |
| `users` | done — the login names in the accounting database, sorted by strcmp, space separated on one line, and NOTHING at all when there are none. FILE is the database and defaults to `/var/run/utmp`; one that is missing, unreadable or a directory is a silent exit 0, which is the whole of this utility's error handling, and a second operand is `extra operand` |
| `who` | done — `-a -b -d -H -l -m -p -q -r -s -t -T/-w -u --lookup`, every record kind, and the row that is one format with pieces left out: NAME and LINE are per-ROW minimum widths of 8 and 12, `-s` is not a last-one-wins flag (the exit column `-a` and `-d` turn on carries the idle and pid columns whatever `-s` said), and the message status and idle time come off `stat(/dev/LINE)` — an absolute ut_line being stat'ed as it stands is what makes those two testable. `--lookup` is getaddrinfo's AI_CANONNAME and canonicalizes only what precedes a colon, the rest being an X display. `ARG1 ARG2` presumes `-m`; a third operand names the THIRD. `--ips`, which #8339 listed, does not exist in GNU 9.4 |
| `pinky` | done — `-l -b -h -p -s -f -w -i -q`, the short format's columns with the passwd database joined on for the real name, and the long format's block per user with `~/.project` and `~/.plan` copied verbatim (a directory of that name opens and reads empty, so the label prints with nothing under it). The login name is NOT trimmed of trailing blanks where `users` and `who -q` trim it. `-l` with no operand is `no username specified…` and exit 1. Its database is `/var/run/utmp` with no operand to redirect it, so the corpus cannot choose what the short format's rows say — see `internal/coreutils/pinky_test.go` |
| `printenv` | done — the whole environment in the vector's own order (so a duplicate name is visible), named variables, `-0`, the in-order option scan, an `=` in a name refused by rule, and gnulib's `exit_failure` of 2. Needs `environ()` |
| `uname` | done — `-a -s -n -r -v -m -p -i -o` and their long spellings including the obsolescent `--sysname` / `--release`, printed in the record's order whatever order they were asked in, and `-a`'s rule that an unknown processor or hardware platform is DROPPED where naming all eight prints `unknown`. `-p` / `-i` are the machine name on Linux, as every distribution's build of GNU reports them |
| `arch` | done — `uname -m` with uname's own option table replaced by the two standard options; every operand is `extra operand` |
| `nproc` | done — the affinity mask by default (so `taskset` changes the answer), the installed count for `--all`, OMP_NUM_THREADS / OMP_THREAD_LIMIT with OpenMP's lenient parse and strtoul's saturation, and `--ignore=N` with its floor of 1 and its non-usage `invalid number` diagnostic |
| `pwd` | done — `-L` `-P`, POSIXLY_CORRECT choosing the default, the three tests $PWD has to pass before `-L` trusts it (absolute, no `.` or `..` component, same dev and inode as `.`), the ignored-operand diagnostic, and the walk up through `..` for a working directory getcwd(2) will not name |
| `base64` `base32` | done — `-d` `-i` `-w`, gnulib's group rules (a fault still writes the bytes the group completed), and the `-w` value's two GNU quirks: a negative is refused, a magnitude past INTMAX_MAX turns wrapping off |
| `basenc` | done — `--base64` `--base64url` `--base32` `--base32hex` `--base16` `--base2msbf` `--base2lsbf` `--z85`, `missing encoding type`, and z85's block requirement on both sides |
| `uniq` | done — `-c` `-d` `-D` `-u` `-i` `-z`, `-f` / `-s` / `-w` and how they compose, `--all-repeated` and `--group` with every separator method, the obsolete `-N` / `+N` operands and the `_POSIX2_VERSION` window that retires the second of them, and the OUTPUT operand, which is a file opened truncating before the input is read. Slower than GNU per line while a substring comparison costs a copy (#8791) |
| `comm` | done — `-1` `-2` `-3` `-z`, `--total`, `--output-delimiter` including the empty one (a NUL) and its refusal of a second, differing one, and the order check in both its shapes: `--check-order` is fatal at the first fault, the default stays silent until an unpairable line has been seen and re-examines a file's last pair at end of input. `comm - -` is ONE stream read alternately, closed twice. Slower than GNU while the order check walks bytes (#8791) |
| `link` | done — one `link(2)`: exactly two operands, no options of its own, and a symlink named as FILE1 linked to ITSELF rather than to what it points at |
| `ln` | done — all four forms with the operand-count rule that decides between them, `-s -b --backup[=CONTROL] -d -F -f -i -L -n -P -r -S -t -T -v`, `VERSION_CONTROL` and `SIMPLE_BACKUP_SUFFIX`, and GNU's replace path: the new link is made beside the destination and RENAMED over it, so a backup is the old destination moved rather than copied and a failure leaves it exactly where it was. `-r` is `lib/canon.fern`'s MISSING walk plus its `relative` |
| `readlink` | done — `-f` `-e` `-m` with their differing strictness, `-n` `-q` `-s` `-v` `-z`, multiple operands, and GNU's cycle rule: not a depth limit, so a 5000-link chain resolves and which name a cycle stops on depends on its length |
| `realpath` | done — `-e` `-m` `-L` `-P` `-s` `-q` `-z`, `--relative-to` and `--relative-base`, on `lib/canon.fern`'s walk |
| `unlink` | done — one `unlink(2)`: exactly one operand, a directory refused with `Is a directory`, and a symlink removed itself rather than followed |
| `mv` | done on the rename path — all three forms with the operand-count rule that decides between them, `-b --backup[=CONTROL] -f -i -n -S -t -T -u --update[=all|none|older] -v -Z --debug --no-copy --strip-trailing-slashes`, `VERSION_CONTROL` and `SIMPLE_BACKUP_SUFFIX`. GNU's order is the substance and no document states it: the rename is tried FIRST, before anything is stat'd, so an occupied destination is EEXIST and everything else is the rename's own errno; `--strip-trailing-slashes` strips the destination too and does it AFTER that first rename; and the same-file rule is three rules, not one, so `mv sl h` between two names for one inode is allowed while `mv d/toh h` is refused. Does NOT copy across filesystems — see the divergences table |
| `rm` | done — `-f` `-i` `-I` `--interactive[=WHEN]` `-r`/`-R` `-d` `-v`, `--one-file-system`, and `--preserve-root[=all]` / `--no-preserve-root` as the two independent flags GNU keeps, plus the hidden `---presume-input-tty` that makes the write-protected prompt reachable from a pipe. fts's trailing-slash trim, the four refusals in GNU's order (EISDIR, ENOTEMPTY, `.`/`..`, the root failsafe by device and inode), the per-file-kind prompts answered by rpmatch, the rule that a failed or first-visit-declined child silences its ancestors, children in raw readdir order, and the leading-hyphen hint every getopt fault inserts before the `Try …` line. A tree deeper than PATH_MAX needs fd-relative traversal (#9074) |
| `rmdir` | done — `-p` `-v` `--ignore-fail-on-non-empty`, the hidden deprecated `--path`, and the two rules a probe turns up: `rmdir sym/` is ENOTDIR with the text replaced by `Symbolic link not followed`, but only for a trailing slash and only when the name's own stat does not already say ENOTDIR; and the ignore flag forgives EACCES/EPERM/EROFS/EBUSY only after reading the directory and finding it non-empty |
| `tty` | done — `-s`, the usage exit of 2 and the write-error exit of 3, on `lib/tty.fern`'s ttyname |
| `runcon` | done on a kernel without SELinux — the current context with no operands whatever options were given, `no command specified`, the `may be used only on a SELinux kernel` refusal, the non-permuting `+r:t:u:l:c` scan and its usage exit of 125. Running a command is refused and says so: see the divergence in `docs/COREUTILS.md` |
| `test` `[` | done — POSIX's one-to-four-argument table and GNU's parser beyond it, every string, integer (any length, compared as digit strings), file and file-pair primary, `-l STRING`, `-t` via isatty, `-r -w -x` against the effective ids; `[` adds the closing `]` and honours `--help` / `--version` as the sole argument where `test` does not. Needs `stat`, `access`, `geteuid` and `isatty`, so it is a native-target utility: WASI reports no mode, owner or effective ids |
| `cut` | done — `-b` `-c` `-f` with GNU's list grammar (overlapping ranges merged, adjacent ones not), `-d` `-n` `-s` `-z`, `--complement` and `--output-delimiter`, the whole-file record a field delimiter equal to the line delimiter makes, and the trailing line delimiter gnulib's first-field buffering decides |
| `paste` | done — parallel and `-s`, the `-d` list with its escapes and its NUL entry that writes nothing, `-z`, the held-back delimiter of a file that ran out, and one shared read position for every `-` operand |
| `fold` | done — `-b` `-s` `-w` and the obsolete `-NUM`, columns counted with tabs, backspaces and carriage returns, and the overflowing character re-measured against the line it lands on |
| `fmt` | done — `-c` `-g` `-p` `-s` `-t` `-u` `-w` and the obsolete `-WIDTH` (argv[1] only), the goal's real default of `width * 187 / 200` rather than the 93% `--help` claims, and the break chooser those steer: a backward dynamic program in HUNDREDTHS of a squared column — the scale is load-bearing, two of the terms are integer divisions — whose seven terms are the squared distance from the goal, a cost per line, half a short cost for the difference between adjacent lines, a sentence-end bonus, the near-refusal of a break after a period that ends no sentence, small bonuses for trailing punctuation and a leading bracket, and two stranded-word terms that shrink with the word's own length. A line reaches WIDTH-1 and never WIDTH, and a paragraph's last line is free, which collects its slack at the FRONT and makes greedy filling the wrong answer. `-s` runs the same program and only stops paragraphs spanning input lines. Startup beats GNU by 3.8x; throughput loses by 6-8x, which is per-word struct and array work rather than the program |
| `expand` | done — `-i` and the `-t` grammar including `/N` and `+N`, the obsolete `-NUM`, `\b` rewinding both the column and the stop cursor, and one space for a tab past the last stop |
| `unexpand` | done — `-a` `--first-only` `-t` and the obsolete `-NUM` (which does NOT imply `-a`, and whose digits and commas spell one list read after the scan), and the rule that a single blank on a stop is held rather than converted |
| `pr` | done — `+FIRST[:LAST]` `-COLUMN` `-a` `-b` `-c` `-d` `-D` `-e` `-f` `-F` `-h` `-i` `-J` `-l` `-m` `-n` `-N` `-o` `-r` `-s` `-S` `-t` `-T` `-v` `-w` `-W`, the digits of `-COLUMN` accumulated over the whole scan (`-2 -3` is twenty-three columns), the column-LOCAL tab stops and display widths a column counts in (a backspace is minus one column, every other non-printable zero until `-c` / `-v` escapes it), a gap of exactly one column always a space even where an output tab would land on it, the last page's columns balanced, the undocumented `-b` GNU accepts and ignores, and a header date read off the FILE's mtime, through `lib/tz.fern` for the zone and gnulib's nstrftime for `-D` |
| `sum` | done — `-r` (the default) and `-s`, whichever comes LAST deciding, with `std/hash`'s two kernels and their 1 KiB / 512-byte block counts rounded up, the name printed for every operand and for none, and a failed input costing its line and the exit status. Startup beats GNU by 5×; `-s` throughput loses by 7× because a byte-sum reduction has no SIMD kernel (#9052) |
| `md5sum` `sha1sum` `sha224sum` `sha256sum` `sha384sum` `sha512sum` | done — `-b` `-c` `-t` `-z` `--tag`, the whole `-c` report (`OK`, `FAILED`, `FAILED open or read`, the improperly-formatted warning and its summary) with `--ignore-missing` `--quiet` `--status` `--strict` `-w`, and GNU's file-name escaping in both directions: a backslash, a newline or a carriage return leads the line with `\` and is written `\\` `\n` `\r` |
| `b2sum` | done — the six above plus `-l BITS` (a multiple of 8 up to 512, base 10) and the `BLAKE2b-NNN` tag, whose length a checksum line carries in base 0 — `BLAKE2b-020` is sixteen bits |
| `cksum` | done — the POSIX CRC by default and `-a` for the other ten (`bsd`, `sysv`, `crc`, `md5`, `sha1`, `sha224`, `sha256`, `sha384`, `sha512`, `blake2b`, `sm3`, matched exactly and not by prefix), `--tag` (the default here) `--untagged` `--base64` `--raw` `-z` `-l BITS`, and `-c` where each line's TAG chooses the algorithm when `-a` did not. The three non-digest algorithms print a checksum and a count, refuse `-c`, and do not escape the name. `--debug` names GNU's chosen CRC implementation and is the third output that is ours by design (`docs/COREUTILS.md`). Startup beats GNU by 5.5x; the CRC throughput row loses by 23x, because GNU folds with `pclmulqdq` (#9056) |
| `dircolors` | done — `-b` / `--sh` / `--bourne-shell`, `-c` / `--csh` / `--c-shell`, `-p`, `--print-ls-colors`, and one compiled-in database serving both `-p` and the default LS_COLORS, because the default is that same text through the ordinary file parser. The TERM / COLORTERM filter is the four-state machine GNU has rather than a flag — a non-matching TERM line closes a section only once an ordinary line has been read since the one that opened it, and the same states decide whether an unknown keyword is fatal or ignored — the pattern is fnmatch(3) with flags 0, and the shell escaper is a toggle. The write-failure wording follows glibc's buffer through `lib/gnu.fern`'s `Stdio`: the shell emitters leave a suffix pending and name the errno, `--print-ls-colors` writes one piece and says a bare `write error`. The database is GNU 9.4's, the corpus's one version-sensitive case. 0.4x GNU on a 200k-entry config at seven heap objects per entry (#9080); faster on every shape anyone runs |
| `du` | done — `-0 -a -b -c -d -h -k -l -m -s -t -x -B -D -H -L -P -S -X`, `--apparent-size --inodes --si --time[=WORD] --time-style --exclude --exclude-from --files0-from --one-file-system`, raw readdir order with directories expanded in place and reported post-order, the (dev, ino) set that counts every inode once and the path-based cycle stopper that takes over when `-l` turns it off, the unit family's last-wins rule with the printed suffix taken off the final DIVISOR, and the two suffix alphabets that are neither the same set nor the set of letters that scale. The walk is an explicit stack in one function: a Map handed to a callee is aliased by the call, so a recursion made the seen set copy the whole table per file. Timestamps render in UTC until a shared localtime lands (#9076), and a path past PATH_MAX is `File name too long` where GNU's fd-relative fts keeps going (#9078) |
| `mktemp` | done — `-d` `-q` `-u` `-t`, `-p DIR` / `--tmpdir[=DIR]` and `--suffix`, with the X run taken from the LAST X in the template (so `fooXXXXbarXXX` replaces three characters and the count is the run's length, not six), gnulib's `file_name_concat` join that keeps an all-slash directory verbatim, the opposite `$TMPDIR` / `-p` precedence `-t` and `--tmpdir` give, `-u` as an lstat retry that reports every errno but ENOENT, the 62^3 retry cap, the created entry removed again when stdout cannot be written, and the hidden short `-V` that its own `--help` does not list |
| `lib/digest.fern` | the seven checksum utilities and the eight digests of `cksum`, which are one program: the option surface, the escaping and the check-line grammar, parameterised by the digest |
| `lib/gnu.fern` | the GNU conventions every utility shares |
| `std/hash` | the three checksums that are not digests — the POSIX CRC-32 and `sum`'s BSD and System V sums — which `cksum -a` reaches and `sum` will |
| `lib/pwdb.fern` | `/etc/passwd` and `/etc/group` as glibc's `files` backend reads them: the lookups by name and id, getgrouplist's ordering, the process's own group set, and the gecos field finger reads as a real name (`&` is the login name capitalised) |
| `lib/utmp.fern` | the login-accounting record for the four utilities that read it — the entry, the scans, and the terminal a ut_line names, which is `lib/tty.fern`'s ttyname minus the `/dev/` prefix the field does not carry — for `logname`, `users`, `who` and `pinky`. The record's tail is per-TARGET: 384 bytes where glibc's `__WORDSIZE_TIME64_COMPAT32` is 1 (x86-64) and 400 where it is 0 (arm64), since `ut_session` and `ut_tv` change width with it |
| `lib/tz.fern` | the local time zone as tzset finds it — TZ as a file name before a POSIX rule, TZif v1/v2/v3 with its footer rule, and the POSIX grammar's three date forms — for `who` and `pinky` |
| `lib/sys.fern` | the five fields of the kernel's utsname record, by name, for `uname` and `arch` |
| `lib/base.fern` | the one encoder / decoder `base64`, `base32` and `basenc` drive, parameterised by alphabet, block and padding |
| `lib/cond.fern` | the conditional expression `test` and `[` evaluate |
| `lib/tabs.fern` | the `-t` tab-stop list `expand` and `unexpand` share: the grammar, its faults, and the next stop past a column |
| `lib/bre.fern` | regular expressions as glibc compiles them: POSIX basic for `expr`, syntax 0 (Emacs) for `tac -r`, anchored or searched over a range of a buffer for `nl` and `csplit`, with a literal and a literal-prefix fast path ahead of glibc's fastmap and the simulation |
| `lib/canon.fern` | the symbolic-link resolution walk `readlink -f/-e/-m` and `realpath` share, under three existence modes, plus the no-symlinks variant `realpath -s` wants. Two rules that are in no man page: a `/`, `/.` or `/..` suffix forces the component before it to be a searchable directory whatever the mode says, and the loop detection is a (parent directory, remaining path) set consulted from the twenty-first link rather than a depth limit — so a 5000-link chain resolves and a cycle's residue depends on its length |
| `lib/tty.fern` | ttyname(3) as glibc answers it: the `/proc/self/fd/N` readlink first, trusted only when it still stats to the same character device, then a walk of `/dev/pts` and `/dev` by device number. `/dev/ptmx` and `/dev/pts/ptmx` share a device number, which is why the order matters. Shared by `tty`, `logname` and `who -m` |
| `lib/selinux.fern` | `is_selinux_enabled()` and `getcon()` as two files rather than libselinux — the selinuxfs line of `/proc/self/mounts` and `/proc/self/attr/current` — for `id` and `runcon` |
| `lib/ld.fern` | C's `float`, `double` and `long double` at the TARGET's formats — strtold, arithmetic, rounding and the `%f` `%e` `%g` `%a` conversions — shared by `printf`, `numfmt`, `seq`, `sleep` and `od` |

The tracking epic (#8278) lists every other utility and its status.
