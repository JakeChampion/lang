# Classic Unix CLI tools, in Fern

Self-contained reimplementations of well-known command-line tools.
Each `.fern` file is a complete program with a header comment that
explains its flags, shows example invocations, and lists the language
features it exercises. They're written to read like the real tools:
they take `argv` flags, read files **or** stdin, write to stdout, send
errors to stderr, and return meaningful exit codes.

These complement the cross-target basics in the parent `examples/`
directory (`hello.fern`, `fizzbuzz.fern`, …) and the wasm/HTTP demos in
`examples/wasm/`. The focus here is the **CLI shape**: argument
parsing, the `$ tool < input` / `$ cat in | tool` pipeline, and
`Result` / `Option` + `match` for fallible I/O.

## Build and run

The native backend needs no external toolchain on Linux, so the
quickest loop is to compile to a native binary and run it:

```
$ fern -target x86-64-linux-linux -o cat examples/cli/cat.fern   # or -target arm64-linux-linux
$ ./cat -n README.md
$ printf 'a\nb\nc\n' | ./cat -n
```

You can also run a tool straight through the interpreter (handy for a
quick check, no binary emitted):

```
$ printf 'one\ntwo\n' | fern -interp examples/cli/tac.fern
$ fern -interp examples/cli/seq.fern -- 1 2 10
```

And they build to wasm like any other program — `-target wasm32-wasi32-wasi`
emits a self-contained preview-2 component (no external adapter):

```
$ fern -target wasm32-wasi32-wasi -o wc.wasm examples/cli/wc.fern
$ wasmtime run --dir=. wc.wasm file.txt
```

## The tools

| File | Mirrors | Flags / behaviour |
|---|---|---|
| `cat.fern`  | `cat`  | `-n` number lines, `-b` number non-blank, `-s` squeeze blanks, `-E` show line ends, `-T` show tabs; files or stdin |
| `tac.fern`  | `tac`  | print lines in reverse order |
| `head.fern` | `head` | `-n N` / `-nN` / `-N` first N lines, `-c N` first N bytes, multi-file banners |
| `tail.fern` | `tail` | `-n N` / `-N` last N lines, `-c N` last N bytes, multi-file banners |
| `wc.fern`   | `wc`   | `-l` lines, `-w` words, `-c` bytes, `-m` UTF-8 chars; per-file rows + total |
| `nl.fern`   | `nl`   | `-b a|t|n` body numbering, `-w` width, `-s` separator, `-v` start, `-i` increment |
| `rev.fern`  | `rev`  | reverse the bytes of each line |
| `seq.fern`  | `seq`  | `seq LAST` / `FIRST LAST` / `FIRST STEP LAST`, `-s` separator, `-w` equal-width |
| `uniq.fern` | `uniq` | collapse adjacent duplicates; `-c` count, `-d` dups only, `-u` unique only, `-i` ignore case |
| `sort.fern` | `sort` | `-r` reverse, `-n` numeric, `-u` unique, `-f` fold case |
| `grep.fern` | `grep -F` | fixed-string search; `-i` ignore case, `-v` invert, `-n` line numbers, `-c` count (bundled flags like `-in`) |
| `cut.fern`  | `cut`  | `-f LIST` fields with `-d DELIM`, `-c LIST` characters; `1,3,5-7` and open ranges |
| `tr.fern`   | `tr`   | translate `SET1`→`SET2` (with `a-z` ranges), `-d` delete, `-s` squeeze |
| `fold.fern` | `fold` | `-w N` wrap width, `-s` break at spaces |
| `tee.fern`  | `tee`  | copy stdin to stdout and to each file; `-a` append |
| `echo.fern` | `echo` | `-n` no trailing newline, `-e` interpret `\n \t \r \\ \0` |
| `yes.fern`  | `yes`  | repeat a line forever (or `-n COUNT` times) |
| `bc.fern`   | `bc`   | a recursive-descent calculator: `+ - * / %`, `^` (power), unary signs, parens, decimals |

## Conventions shared across the tools

- **Input resolution.** A `-` operand (or no operands at all) means
  stdin; everything else is a file path. Tools call
  `io.read_input(path): Result[string, IoError]` from `std/io`, which
  picks stdin or a file and lets the caller `match` `Ok` / `Err` to
  report a missing file and keep going. (`read_input` is itself a thin
  wrapper over `read_all_stdin()` + the `read_file` builtin — added
  alongside these examples to kill the per-tool boilerplate.)
- **Stdin.** Whole-input tools read via `io.read_input("-")` (i.e.
  `read_all_stdin()`); line-oriented ones then `.lines()` the result.
  (`examples/wasm/wc.fern` shows the alternative streaming
  `Reader.read_line()` loop.)
- **Number flags.** Integer operands (`head -n N`, `seq STEP`,
  `fold -w N`, …) parse with `s.parse_int_or(fallback)` from
  `std/string` — `parse_int` with a default baked in.
- **Output.** `print(s)` adds a newline; `write(s)` does not (used when
  a tool must control its own line endings, e.g. `cat`/`head -c`,
  `tr`). Diagnostics go to `eprint`.
- **Exit codes.** Tools follow the usual conventions — e.g. `grep`
  returns 0 on a match, 1 on no match, 2 on a bad option, and `bc`
  returns 1 if any expression was invalid.
