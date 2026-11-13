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
`-l N`, `-z`, `-u` and `-b`. The long options `--posix`, `--follow-symlinks`,
`--sandbox`, `--help` and `--version` are also in the port. The port reads
POSIXLY_CORRECT as GNU sed does.

The regular expressions are the BRE and ERE of GNU sed, with character
classes and back references. The GNU operators are `\+`, `\?`, `\|`, `\w`,
`\W`, `\s`, `\S`, `\b`, `\B`, `\<`, `\>`, `` \` `` and `\'`. An empty
regex stands for the last regex that ran.

The exit status is the code of `q` or `Q`, or one of these:

- 0 when the run is correct.
- 1 for an error in the script.
- 2 when sed cannot read an input file.
- 4 for an error in the run.

## What the port does not do

- `--debug`. GNU sed then prints the script and a trace of each cycle in a
  form of its own. The port stops with exit status 1 and the message
  `this port does not have the option '--debug'`.
- Multibyte characters. The port works on bytes, as GNU sed does in the C
  locale. The corpus runs in the C locale.
- A write error in the middle of a large output names the bytes of the
  port's buffer. GNU sed names the items of its last write to stdio.

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

The input is a file of 100 MiB: 2,295,335 lines of 3 to 14 words from a set
of 32 words. The file is in the page cache, and the output goes to
`/dev/null`. Each value is the best of seven runs, with the runs of the two
programs in turn, on a 4-core x86-64 container, 2026-11-13. Other jobs ran
on the container at the same time, so a value can change by 20% from one set
of runs to the next. The two programs give the same output for each script.

| Script | GNU sed 4.9 | Fern sed |
|---|--:|--:|
| `s/foo/bar/g` | 0.379 s | 0.220 s |
| `-n '/lazy dog/p'` | 0.247 s | 0.190 s |
| `-n '/[0-9]x\|q[a-z]*z/p'` | 0.167 s | 0.180 s |
| `y/abcdefghij/ABCDEFGHIJ/` | 0.195 s | 0.180 s |
| `-n '1000000,1100000p'` | 0.084 s | 0.062 s |
| `-n '$p'` | 0.099 s | 0.071 s |
| `-n '$='` | 0.085 s | 0.063 s |
| `p` | 0.169 s | 0.134 s |
| `d` | 0.086 s | 0.077 s |
| `s/\(fox\) \(\w*\)/\2 \1/g` | 0.626 s | 0.491 s |
| `-E 's/(a\|e)+/X/g'` | 5.821 s | 1.767 s |

The port is faster on each script but one. On the regex
`[0-9]x\|q[a-z]*z` the two programs are equal within the noise:
`perf stat` over nine runs gives 178 ms to 200 ms for the port and 198 ms to
214 ms for GNU sed. No literal string starts a match of that regex. Thus
the port finds each byte that can start a match with the set scan of the
runtime. The scan takes one third of the time, and it reads one byte at a
time.

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
- A regex that few bytes can start runs the DFA only from those bytes.
- A regex with at most two repetitions and no repeated group gets its
  groups from a walk to the end of the match, without the thread list.
- On x86-64, the runtime maps an input of fewer than 256 bytes with the
  scalar loop. The AVX2 loop with sixteen table lookups cost more than the
  scalar loop on the short lines of `y`.
