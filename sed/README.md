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
