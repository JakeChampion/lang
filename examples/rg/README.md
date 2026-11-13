# rg

A port of [ripgrep](https://github.com/BurntSushi/ripgrep) 14.1.0 to Fern
(#15590). It searches files for a regular expression with `std/regex`. The
port implements the behaviour that ripgrep shows. It does not copy source
code from ripgrep.

```
$ fern -O -target x86-64-linux -o rg examples/rg/rg.fern    # or arm64-linux
$ ./rg -n -C1 foo a.txt b.txt
$ cat a.txt | ./rg -c -e foo -e bar
```

## What it has

This is slice 1 of the port. It searches one file, the files on the command
line, or the standard input (`-`, or no file).

| Option | Long form | Effect |
| --- | --- | --- |
| `-e P` | `--regexp P` | A pattern. Give it more than once for more patterns. |
| `-i` | `--ignore-case` | Ignore case. `-s` (`--case-sensitive`) turns it off. |
| `-v` | `--invert-match` | Select the lines that do not match. |
| `-w` | `--word-regexp` | Match only between `\b{start-half}` and `\b{end-half}`. |
| `-F` | `--fixed-strings` | Each pattern is a literal. |
| `-n`, `-N` | `--line-number`, `--no-line-number` | Line numbers on or off. |
| `-c` | `--count` | The count of selected lines, or with `-o` of matches. |
| `-l` | `--files-with-matches` | The names of the files with a selected line. |
| `-o` | `--only-matching` | Each match on a line of its own. |
| `-A N`, `-B N`, `-C N` | `--after-context`, `--before-context`, `--context` | Context lines. `-A` and `-B` take precedence over `-C`. |
| `-H`, `-I` | `--with-filename`, `--no-filename` | File names on or off. |
| | `--color never\|auto\|always\|ansi` | Color, with the default colors of ripgrep. |
| `-j N` | `--threads N` | The thread count. The port searches with one thread. |

Options and paths can come in any order. `--` ends the options. Short options
can share one dash, and a value can follow its letter, as in `-nA1`.

The output is that of ripgrep when its standard output is not a terminal:
no heading, and no line numbers without `-n`. These rules are the same as in
ripgrep:

- A file name comes before each line when there is more than one path, or
  with `-H`. The standard input is `<stdin>`.

- A match line has `:` after its name and number, and a context line has
  `-`. A `--` line comes between groups of lines that are not adjacent, in a
  file and from file to file. With no context, there is no `--`.

- A last line without a line terminator gets one. A CRLF line keeps its CR.

- With `-o`, an empty match is an empty line. An empty match that starts where
  the last match ends does not count.

- A file that holds a NUL byte is binary. When it has a selected line, the
  port writes `binary file matches (found "\0" byte around offset N)` in
  place of the lines. `-c` and `-l` count and name binary files as text.

- The exit status is 0 when a line is selected, 1 when none is, and 2 after
  an error, also when lines are selected.

- A file that does not exist gives `rg: PATH: No such file or directory (os
  error 2)`. With one thread (`-j1`, or one CPU), ripgrep writes
  `rg: PATH: IO error for operation on PATH: ...`, and the port does the same.

- The messages for an unknown flag, a missing value, an invalid count and an
  invalid color are those of ripgrep.

## Differences from ripgrep

- **Bytes and ASCII.** `std/regex` reads bytes. `.` matches one byte, and
  `\w`, `\d`, `\s`, `\b`, `-w` and `-i` know only ASCII. ripgrep uses
  Unicode for each of these. `\p{...}`, inline flags other than `(?i)` at the
  start, and lazy repetition are not in `std/regex`.

- **Pattern errors.** A pattern that does not compile gives exit status 2, as
  in ripgrep, but the message is that of `std/regex`.

- **Binary files.** The port examines the whole file as one buffer. ripgrep
  reads 64 KiB at a time, or maps the file into memory. The first NUL byte
  can be after the first 64 KiB. Then ripgrep writes the lines that it found
  before that buffer, and the port does not.

- **Not in this slice.** The search of a directory, ignore files, `-g`,
  `-t`, the parallel search, `--json`, the heading on a terminal, `-q`, `-x`
  and `-m`. With no path, the port reads the standard input. ripgrep reads it
  only when it is not a terminal.

## Tests

- `rg_test.fern`: the options, the errors, the pattern, the context, the
  separators, the counts, the color and the binary files.
  `TestSelfHostExampleRgUnit` runs it on x86-64, arm64 and wasm32-wasi with
  the leak census.

- `TestSelfHostExampleRgAgainstRipgrep` runs the port and `/usr/bin/rg`
  14.1.0 with 145 command lines on the same files, on x86-64 and on arm64.
  The stdout, the stderr and the exit status must be the same. The files
  include an empty file and a file with no last line terminator. They also
  include CRLF lines, a line of 209,008 bytes, UTF-8 text and four binary
  files. The cases include each option, a missing file, the standard input
  and the errors.

  The test fails when `/usr/bin/rg` is not ripgrep 14.1.0. A case with more
  than one file gives `-j1`, because with more threads ripgrep writes the
  files in the order that their searches end.

## Performance

The goal of #15590 is a port that is faster than ripgrep. This slice records
where the port is, and why.

The host is an Intel Xeon at 2.10 GHz with 4 CPUs and 16 GB of memory, Linux
6.18 on x86-64. The text is `corpus 200`: 209,715,214 bytes and 4,762,203
lines from `corpus.fern`, in the page cache. About one line in a hundred holds
a name or a Greek word. The four patterns are a literal, the same literal
with `-i`, an alternation of five names, and `\w+`.

```
$ fern -O -target x86-64-linux -o corpus examples/rg/corpus.fern
$ ./corpus 200 > big.txt
```

### std/regex without rg

`regex_bench.fern` times `std/regex` alone, on the text in memory. `lines`
calls `is_match` on each line, as `rg -c` counts. `find` counts the matches
in the whole text, as `rg -co` does. The time is the best of 3 runs, in
milliseconds. ripgrep's time is its whole run, with the read of the file.
"Before" is `std/regex` before this slice, and "after" adds the first-byte
skip and the literal prefix.

"Pike VM" is the leftmost-first search. It gets each span from the Pike VM,
and not from a walk of the syntax tree. It runs the VM again from each
candidate start. "Flat VM" runs the VM in one pass over the text, with its
threads in one array of words, and `count` makes no array of matches.

"DFA" finds each span with a forward DFA and a reverse DFA that `compile`
builds. When no match can be empty, `count` needs only the ends of the
matches, so it does not run the reverse DFA. `rg -co` writes each span, so
the port runs both DFAs. The "Pike VM", "Flat VM", "DFA" and "ripgrep"
columns ran one after the other in one session.

| Pattern | Mode | Before | After | Pike VM | Flat VM | DFA | ripgrep |
| --- | --- | ---: | ---: | ---: | ---: | ---: | ---: |
| `Sherlock Holmes` | lines | 8,269 | 107 | 130 | 140 | 108 | 44 |
| | find | 7,875 | 30 | 46 | 32 | 24 | 46 |
| `(?i)sherlock holmes` | lines | 9,138 | 364 | 418 | 404 | 303 | 118 |
| | find | 8,425 | 276 | 288 | 286 | 198 | 146 |
| `Sherlock Holmes\|John Watson\|Irene Adler\|Inspector Lestrade\|Professor Moriarty` | lines | 40,615 | 267 | 396 | 309 | 228 | 110 |
| | find | 38,936 | 230 | 275 | 209 | 143 | 120 |
| `\w+` | lines | 409 | 394 | 765 | 464 | 123 | 319 |
| | find | 7,336 | 7,140 | 13,727 | 7,841 | 1,497 | 3,249 |

The counts are the same as those of ripgrep, but for `\w+` in `find` mode:
47,078,742 against 45,612,443, because `std/regex` knows only ASCII word
bytes. ripgrep has no `\p{Greek}` peer to compare, because `std/regex` has
no Unicode classes.

Before this slice, `std/regex` ran its matcher at each byte of the text. The
matcher walked the syntax tree and made a new array for each node at each
position. So a literal ran at 25 MB/s. Now a search goes from one possible
start to the next with `__memchr` or `__scan_set`, and checks the literal
prefix before it runs the matcher.

### The port and ripgrep

Each command searches `big.txt` with `-j1` and writes to a file. The time
is the best of 5 runs, in milliseconds. The port finds each span with the
DFAs of `std/regex`. The two programs ran one after the other in the same
session.

| Command | ripgrep | Port | Ratio |
| --- | ---: | ---: | ---: |
| `rg 'Sherlock Holmes'` | 46 | 266 | 5.8 |
| `rg -c 'Sherlock Holmes'` | 42 | 268 | 6.4 |
| `rg -i 'sherlock holmes'` | 129 | 526 | 4.1 |
| `rg -ic 'sherlock holmes'` | 117 | 479 | 4.1 |
| `rg 'Sherlock Holmes\|...'` (five names) | 120 | 384 | 3.2 |
| `rg -c 'Sherlock Holmes\|...'` | 110 | 374 | 3.4 |
| `rg -c '\w+'` | 398 | 404 | 1.0 |
| `rg -co '\w+'` | 3,356 | 2,471 | 0.74 |

Each output is the same as that of ripgrep, but `-co '\w+'`, for the
reason above.

### Why the port is slower on literals

1. **The read of the file.** For `rg -c 'Sherlock Holmes'`, about 240 ms
   of the 271 ms is the input. `read_file_bytes` takes about 200 ms: one
   `read` of about 115 ms into a new buffer, and then a copy of the 200 MiB
   into a new array (`compiler/runtime/*/__fern_read_file_bytes_u8.fern`).
   `string_from_bytes_unchecked` copies the bytes again, in about 40 ms.

   ripgrep maps the file into memory and does not copy it. The fix is in the
   runtime: read into the storage of the array, or map the file, and make a
   string from a unique array without a copy.

2. **The scan for a start.** When more than one byte can start a match,
   `__scan_set` reads four bytes in each turn with a table. `-i` scans for
   `s` and `S`, and the alternation scans for `S`, `J`, `I` and `P`. ripgrep
   uses SIMD kernels for two and three bytes and for small sets of literals.
   The fix is a SIMD kernel for a small byte set, or a scan for the least
   frequent byte of the literal.

3. **The matcher is not the cause.** `std/regex` finds each span with two
   DFAs, and a step of a DFA is one lookup in a table. With `\w+`, where
   the search is most of the work, the port is faster than ripgrep. The
   code that the compiler makes for the loop of a DFA can use fewer
   instructions: it keeps each `i32` of the table in 8 bytes, and it
   checks the bounds of each lookup.

The line loop is not the cause. Without `-v`, the port goes from one
candidate to the line of the next with `next_candidate`, and counts the
lines that it skips with `__count_byte`. The lines without a candidate cost
nothing more.
