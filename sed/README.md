# sed

GNU sed, written again in Fern. The port gives the same output, the same
error messages and the same exit codes as GNU sed 4.9.

The port uses the shared library of the coreutils ports:
`coreutils/lib/gnu.fern` for the options and the messages, and
`coreutils/lib/bre.fern` for the regular expressions. The parity gate is the
coreutils corpus in `internal/testing/coreutils/`. `docs/COREUTILS.md` has
the rules of parity and the method.

## Build

```
$ fern -O -target x86-64-linux -o sed sed/sed.fern     # or arm64-linux, arm64-darwin
$ printf 'hello world\n' | ./sed 's/\(\w*\) \(\w*\)/\2 \1/'
world hello
$ ./sed --version
sed (Fern sed) 0.1
```

## What the port does

The script language:

- The addresses: a line number, `$`, `/RE/` and `\cREc` with the `I` and
  `M` flags, `first~step`, `addr1,+N`, `addr1,~N`, `0,/RE/` and `!`.
- The commands that edit: `s`, `y`, `g`, `G`, `h`, `H`, `x`, `z`, `d`, `D`,
  `n` and `N`.
- The commands that write: `p`, `P`, `l`, `=`, `F`, `a`, `i`, `c`, `r`, `R`,
  `w`, `W` and `e`.
- The control commands: `b`, `t`, `T`, `:`, `{`, `}`, `q`, `Q`, `#`, `v`,
  and `#n` on the first line.

The parts of `s` and the escapes:

- The flags of `s`: `g`, a number, `p`, `w FILE`, `i` and `I`, `m` and `M`,
  and `e`.
- In a replacement: `&`, `\0` to `\9`, `\L`, `\U`, `\l`, `\u`, `\E` and a
  newline.
- The escapes `\a`, `\f`, `\n`, `\r`, `\t`, `\v`, `\cX`, `\dNNN`, `\oNNN` and
  `\xHH` in a regex, a replacement, `y` and a text.

The options are `-n`, `-e`, `-f`, `-E` and `-r`, `-s`, `-i[SUFFIX]`,
`-l N`, `-z`, `-u` and `-b`. The long options `--debug`, `--posix`,
`--follow-symlinks`, `--sandbox`, `--help` and `--version` are also in the
port.

`--debug` prints the program before the run, and a trace of each cycle: the
input line, each command that the run gets to, and the pattern space or the
hold space after a command that changes it. The trace has the form of GNU
sed 4.9, byte for byte, with its quirks. For example, the depth of the
blocks goes on from one cycle to the next. Also, after the first run of `e`,
the newline at the end of its text is a NUL. The port reads
POSIXLY_CORRECT as GNU sed does.

In a UTF-8 locale the port reads characters of UTF-8, as GNU sed does. The
locale is the first of LC_ALL, LC_CTYPE and LANG that is set, and its
codeset must be UTF-8, as in `C.UTF-8`.

The locale must also be on the system. Each category of the locale comes
from LC_ALL, its own variable or LANG, and each one must load. When one
category does not load, the port uses the C locale, as GNU sed does. The
port finds a locale as the libc does:

- glibc looks in the locale archive, and then in the directories of
  LOCPATH and in `/usr/lib/locale`. A name can lose its territory and its
  modifier, so `C_XX.UTF-8@euro` finds `C.utf8`.
- For glibc, the codeset must be a name of UTF-8 for gconv: `UTF-8` or
  `UTF8`, in upper or lower case.
- Darwin looks for the name as it is, in PATH_LOCALE or
  `/usr/share/locale`.
- On WASI and on Android, the libc reads no files for a locale. There the
  codeset of the name decides.

In a UTF-8 locale:

- `.`, a bracket expression, `\w`, `\W`, `\s` and `\S` match one
  character. A byte that starts no character matches only itself, outside a
  bracket expression.
- The classes and the case conversions of `s` and `I` come from the tables
  of glibc 2.39 in C.UTF-8. `scripts/gen-glibc-ctype.py` writes them to
  `coreutils/lib/glibc_ctype.fern` from the libc of the host. For example,
  `[[:alpha:]]` holds the decimal digits outside ASCII, and U+0363 is in
  `[[:punct:]]`.
- `y` maps characters, and `--debug` prints its pairs in their order.
- The word assertions read the character before and after each position.
- `l` and the trace of `--debug` still show bytes.

On Linux, GNU sed has the regex of glibc. There a range in a bracket
expression takes two ASCII ends, as in C.UTF-8. On the other targets, GNU
sed has the regex of gnulib. There an end can be any character, and the
range holds the code points from one end to the other.

In the C locale each byte is a character. In glibc, a byte above 127 has
no case. There `\U`, `\L`, `\u` and `\l` change the byte to 255. On
Darwin, these escapes do not change a byte above 127.

Under the `I` flag, the port matches in the two steps of GNU sed:

- When a search starts at the first byte, the dfa of GNU sed reads the
  whole text first. The dfa takes each character with its counterparts
  from gnulib: its upper case, the lower case of that, and a list of lone
  lower cases. A line that the dfa rejects has no match.
- Then the regex of glibc finds the match. It compares the upper case of
  the text with the pattern in upper case.

The escaped bytes and the names of classes keep their case in the regex.
Thus `\q` matches nothing, and `[Z-a]` is an invalid range.

The two steps disagree only for some characters and ranges. In a UTF-8
locale, the counterparts leave out U+1C80 to U+1C88. Thus `s/т/X/I` changes
`ᲄ т` to `X т`, but it does not change `ᲄ`. In the C locale, the dfa reads
a range before the upper case, so `[a-C]` matches nothing. The port runs
the dfa step only for a regex that has such a character or range.

The regular expressions are the BRE and ERE of GNU sed, with character
classes and back references. The GNU operators are `\+`, `\?`, `\|`, `\w`,
`\W`, `\s`, `\S`, `\b`, `\B`, `\<`, `\>`, `` \` `` and `\'`. An empty
regex stands for the last regex that ran.

The exit status is the code of `q` or `Q`, or one of these:

- 0 when the run is correct.
- 1 for an error in the script.
- 2 when sed cannot read an input file.
- 4 for an error in the run.

The port writes standard output in blocks of 64 KiB. GNU sed writes it
through stdio, in blocks of the block size of the file, at most 8 KiB. The
port keeps a record of the calls of GNU sed to stdio after its last block:
the length of each call, and whether GNU sed checks it for an error.

After a failed write, the port finds the call that filled the block of
stdio at the failed byte. It gives the message of that call, with the same
count of items. After a call that GNU sed does not check, the port drops
the bytes that stdio drops, and continues. The port writes each block at
once, as GNU sed does, when the record cannot show the same result: with
`--debug`, `-i` or `-u`, with more than one input, with a w file that is
not standard output, and with `e`, `=` or `F`.

## What the port does not do

These differences remain in a UTF-8 locale:

- The port does not read the aliases of `locale.alias`, and it does not
  read the codeset in the files of a locale. It takes the codeset from the
  name.
- On Darwin, GNU sed takes the classes from the libc of Darwin. The port
  has the tables of glibc on each target. For example, U+0663 is in
  `[[:digit:]]` on Darwin and in `[[:alpha:]]` in glibc.

These differences come from the regex of glibc:

- A byte that continues a character but follows a complete one can move a
  match that has a word assertion. Then GNU sed can start the match inside
  the character before it. For example, `printf '\xbf b' | sed
  's/\W*\B/[&]/g'` gives `[]\xbf []b` in GNU sed and `[]\xbf[] b` in the
  port.
- The cause is a step of glibc that the port does not copy. glibc builds
  the context of a search again when the search starts inside a character.
- Under `I`, the same step of glibc can put a match inside a character
  whose upper case has a different length. For example, `echo ı | sed
  's/\>/X/I'` writes the `X` between the two bytes of `ı` in GNU sed.

These differences come from the output and from WASI:

- With `--debug`, a write error on standard output can give a different
  message. GNU sed writes the trace in many small calls to stdio. The record
  of the port takes each byte of the trace as one call.
- On wasm32-wasi, the `e` command and the `e` flag stop with an error,
  because WASI has no processes.

## Tests

The corpus is `internal/testing/coreutils/sed_test.go`. It has more than
600 cases. The gate runs each case three times:

- The port and GNU sed, byte for byte, with the tree after the run.
- The port from the self-host compiler and the port from the release
  compiler.
- The port with the leak census on.

The oracle is the GNU sed in `FERN_GNU_SED`, a PATH-style list. Without it,
the harness looks in `PATH`, `/usr/bin`, `/bin` and `/usr/local/bin`. CI
builds GNU sed 4.9 from source with `.github/actions/gnu-sed`.

The cases come from the documented behaviour of GNU sed and from runs of the
GNU binary. No case comes from the test suite of GNU sed.

## Performance

The input is a file of 100 MiB: 2,205,598 lines of 3 to 14 words from a set
of 32 words. The file is in the page cache, and the output goes to
`/dev/null`. Each value is the best of seven runs, with the runs of the two
programs in turn, on a 4-core x86-64 container, 2026-11-15.

The load of the container was 7 to 11 during the runs. Thus a wall time
can change by 20% from one set of runs to the next. The CPU time is the
user time and the system time of the program, and it changes less. The two
programs give the same output for each script.

| Script | GNU sed 4.9 | Fern sed | GNU CPU | Fern CPU |
|---|--:|--:|--:|--:|
| `s/foo/bar/g` | 0.436 s | 0.314 s | 0.435 s | 0.313 s |
| `-n '/lazy dog/p'` | 0.356 s | 0.283 s | 0.352 s | 0.279 s |
| `-n '/[0-9]x\|q[a-z]*z/p'` | 0.220 s | 0.227 s | 0.218 s | 0.226 s |
| `y/abcdefghij/ABCDEFGHIJ/` | 0.260 s | 0.303 s | 0.259 s | 0.280 s |
| `-n '1000000,1100000p'` | 0.110 s | 0.109 s | 0.108 s | 0.106 s |
| `-n '$p'` | 0.116 s | 0.106 s | 0.115 s | 0.105 s |
| `-n '$='` | 0.120 s | 0.112 s | 0.118 s | 0.106 s |
| `p` | 0.304 s | 0.246 s | 0.243 s | 0.195 s |
| `d` | 0.139 s | 0.146 s | 0.107 s | 0.113 s |
| `s/\(fox\) \(\w*\)/\2 \1/g` | 0.985 s | 0.784 s | 0.832 s | 0.736 s |
| `-E 's/(a\|e)+/X/g'` | 9.964 s | 2.835 s | 9.346 s | 2.764 s |

The port is faster on eight scripts. On `y`, on `d` and on the regex
`[0-9]x\|q[a-z]*z` it is not. callgrind counts the instructions on the
first 10 MB of the input. For `y`, GNU sed runs 213 million and the port
259 million. For `d`, GNU sed runs 71 million and the port 86 million.

Thus the port spends more on each line before the script runs: about 400
instructions for `d`. Half of that is the copy of the line into the
pattern space, with its allocation and its release.

The regex `[0-9]x\|q[a-z]*z` has no literal string at the start of a match.
The port now finds each byte that can start a match with `__scan_masks`.
The scan tests 16 or 32 bytes at a time, and no longer one byte. That takes the
script from 257 ms to 213 ms of CPU time. The rest is the cost of each
line.

These changes made the port faster.

The changes in the port:

- Each cycle gives the state of the run to the next step through `own`
  parameters. Thus each step changes the state in place, and does not copy
  the record of the run for each line.
- The `s` command finds the groups of a match only when its replacement
  uses them.

The changes in the shared code:

- In `coreutils/lib/bre.fern`, a regex that is one literal string gives
  its match from the literal search, without the DFA.
- A regex with no literal at its start finds the bytes that can start a
  match with the SIMD probes of `__scan_masks_bytes`.
- A regex that few bytes can start runs the DFA only from those bytes.
- A regex with at most two repetitions and no repeated group gets its
  groups from a walk to the end of the match, without the thread list.
- On x86-64, the runtime maps an input of fewer than 256 bytes with the
  scalar loop. The AVX2 loop with sixteen table lookups cost more than the
  scalar loop on the short lines of `y`.
