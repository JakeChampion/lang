# rg

A port of [ripgrep](https://github.com/BurntSushi/ripgrep) 14.1.0 to Fern
(#15590). It searches files for a regular expression with `std/regex`. The
port implements the behaviour that ripgrep shows. It does not copy source
code from ripgrep.

```
$ fern -O -target x86-64-linux -o rg examples/rg/rg.fern    # or arm64-linux
$ ./rg -n -C1 foo a.txt b.txt
$ ./rg -t rust -g '!target' fn
$ cat a.txt | ./rg -c -e foo -e bar
```

## What it has

The port searches the files on the command line, each file under each
directory on the command line, or the standard input (`-`). With no path, it
reads the standard input when that is a file, a pipe or a socket. Else it
searches the current directory, and it writes each name without the leading
`./`.

### The search

| Option | Long form | Effect |
| --- | --- | --- |
| `-e P` | `--regexp P` | A pattern. Give it more than once for more patterns. |
| `-i`, `-s`, `-S` | `--ignore-case`, `--case-sensitive`, `--smart-case` | The case rule. The last one wins. `-S` ignores the case when no pattern has a capital letter. |
| `-v` | `--invert-match` | Select the lines that do not match. |
| `-w` | `--word-regexp` | Match only between `\b{start-half}` and `\b{end-half}`. |
| `-x` | `--line-regexp` | Match only a whole line. |
| `-F` | `--fixed-strings` | Each pattern is a literal. |
| `-m N` | `--max-count N` | Stop the search of a file after N selected lines. |
| `-a` | `--text` | Search binary files as text. |
| | `--binary` | Search a binary file that the walk finds as a file on the command line. |
| | `--mmap`, `--no-mmap` | Map the files into memory, or read them in buffers. This changes the rules for binary files. |
| `-j N` | `--threads N` | The count of workers. |

### The output

| Option | Long form | Effect |
| --- | --- | --- |
| `-n`, `-N` | `--line-number`, `--no-line-number` | Line numbers on or off. |
| `-c` | `--count` | The count of selected lines, or with `-o` of matches. |
| | `--count-matches` | The count of matches. |
| | `--include-zero` | With `-c`, also the files with a count of 0. |
| `-l` | `--files-with-matches` | The names of the files with a selected line. |
| | `--files-without-match` | The names of the files with no selected line. |
| `-q` | `--quiet` | No output. The search stops at the first match. |
| `-o` | `--only-matching` | Each match on a line of its own. |
| `-A N`, `-B N`, `-C N` | `--after-context`, `--before-context`, `--context` | Context lines. `-A` and `-B` take precedence over `-C`. |
| | `--context-separator S`, `--no-context-separator` | The line between groups of context, with the escapes `\n`, `\t` and `\xHH`, or no line. |
| `-H`, `-I` | `--with-filename`, `--no-filename` | File names on or off. |
| | `--heading`, `--no-heading` | The file name on a line of its own, before the lines of the file. |
| `-0` | `--null` | A NUL byte after each file name. |
| | `--path-separator S` | The byte that takes the place of `/` in a file name. |
| | `--color never\|auto\|always\|ansi` | Color, with the default colors of ripgrep. |

### The walk

| Option | Long form | Effect |
| --- | --- | --- |
| | `--files` | The names of the files to search, with no search. |
| `-.` | `--hidden` | Search hidden files and directories. |
| `-L` | `--follow` | Follow symbolic links. |
| `-d N` | `--max-depth N` | Go down at most N directories. |
| | `--max-filesize N` | Search no walked file larger than N bytes. `K`, `M` and `G` after N multiply it by 1024 once, twice or three times. |
| `-g G` | `--glob G` | Search only the files that match a glob. A glob with `!` excludes. |
| | `--iglob G`, `--glob-case-insensitive` | The globs ignore the case. |
| `-t T`, `-T T` | `--type T`, `--type-not T` | Search only the files of a type, or not those files. `all` is each type. |
| | `--type-add D`, `--type-clear T`, `--type-list` | Add to a type, clear a type, or write the types. |
| | `--sort S`, `--sortr S` | Sort by `path`, `modified`, `accessed` or `created`, up or down. A sort uses one thread. |
| | `--no-ignore`, `--ignore` | Ignore files off or on. |
| | `--no-ignore-vcs`, `--no-ignore-dot`, `--no-ignore-parent`, `--no-ignore-exclude`, `--no-ignore-global`, `--no-ignore-files` | One kind of ignore file off. Each has a form without `no-`. |
| | `--ignore-file F` | More ignore rules. |
| | `--no-require-git` | Use `.gitignore` files also outside a git repository. |
| `-u`, `-uu`, `-uuu` | `--unrestricted` | `--no-ignore`, then also `--hidden`, then also `--binary`. |
| | `--no-messages`, `--no-ignore-messages` | No error messages, or no messages about ignore files. |

Options and paths can come in any order. `--` ends the options. Short options
can share one dash, and a value can follow its letter, as in `-nA1`.

The output is that of ripgrep when its standard output is not a terminal:
no heading, and no line numbers without `-n`. These rules are the same as in
ripgrep:

- A file name comes before each line when the search is of more than one
  file, or with `-H`. The standard input is `<stdin>`.

- A match line has `:` after its name and number, and a context line has
  `-`. A `--` line comes between groups of lines that are not adjacent, in a
  file and from file to file. With no context, there is no `--`. With
  `--heading`, an empty line comes between files.

- A last line without a line terminator gets one. A CRLF line keeps its CR.
  A UTF-8 byte order mark at the start of a file is not searched.

- With `-o`, an empty match is an empty line. An empty match that starts where
  the last match ends does not count.

- With `-m N` and context, the after-context of the last match continues. A
  match in that context counts as a context line, but it has `:`.

- The exit status is 0 when a line is selected, 1 when none is, and 2 after
  an error, also when lines are selected. With `-q`, a selected line gives 0
  also after an error.

- A file that does not exist gives `rg: PATH: No such file or directory (os
  error 2)`. With one thread (`-j1`, `--sort`, or one file), ripgrep writes
  `rg: PATH: IO error for operation on PATH: ...`, and the port does the same.

- The messages for an unknown flag, a missing value, an invalid count, an
  invalid choice, an invalid glob and an unknown type are those of ripgrep.
  When a walk with no path finds no file, the port writes the message of
  ripgrep about filters.

### The ignore rules

The walk reads `.gitignore`, `.ignore` and `.rgignore` in each directory, and
`.git/info/exclude` in a git repository. For a worktree, it finds the exclude
file from the `gitdir:` line of the `.git` file. It also reads the global
gitignore: `core.excludesFile` of `$HOME/.gitconfig`, else
`$XDG_CONFIG_HOME/git/ignore` or `$HOME/.config/git/ignore`. The precedence is
that of the ignore crate of ripgrep:

1. The `-g` globs. When a glob without `!` is given, a file that no glob
   matches is ignored.
2. `.rgignore`, then `.ignore`, then `.gitignore`, then the exclude file,
   then the global gitignore, then `--ignore-file`. In each kind, the file
   of the innermost directory wins, and in a file the last matching line
   wins.
3. The `-t` and `-T` types.
4. A hidden file is ignored unless a rule above keeps it.

A `.gitignore` applies only in a git repository: a directory with a `.git`
directory or file, or a directory below one. The walk does not use a
`.gitignore` above the root of the repository. The ignore files of the
directories above the root of the walk also apply. A path on the command
line is searched whatever the rules say.

The globs have the syntax of the globset crate, which `glob.compile` in
`std/glob` implements. The file types are those that ripgrep 14.1.0 writes
with `--type-list` (`types.fern`).

### Binary files

The port finds binary data as ripgrep does. A file that a walk finds is
"implicit", and a file on the command line is "explicit".

- ripgrep maps an explicit file into memory when the command line has at
  most ten paths and all of them are files. Then it examines the first 64 KiB
  and each line that it writes for a NUL byte. When it finds one, the next
  line that it writes stops the search, and it writes `binary file matches
  (found "\0" byte around offset N)`.

- Else ripgrep reads the file in buffers. The first buffer has 64 KiB, and
  it grows to three times its size when a line does not fit. The next file
  of the same thread keeps the grown buffer. In an explicit file, each NUL
  byte from the buffer that holds the first one becomes a line terminator.
  In an implicit file, the search stops before that buffer. It writes
  `WARNING: stopped searching binary file after match ...` when it found a
  line before. With `-c`, `-l` and `--files-without-match`, an implicit
  binary file has no output.

The port computes the offsets of the buffers from the size of the file and
its line terminators.

### Parallel search

With more than one thread, the port walks the tree first. Then it gives
each worker process from `std/worker` every Nth file, and it writes the
output of the workers in turn. The output of each file stays together, as
in ripgrep. The order of the files is not that of the walk, and ripgrep
also writes the files in no fixed order. Use `-j1` or `--sort path` for the
order of the walk.

## Differences from ripgrep

- **Unicode tables.** `std/regex` uses the tables of Unicode 17.0.0 from
  Go's `unicode` package, and ripgrep 14.1.0 uses Unicode 15.1. A script
  takes its long name, as `\p{Greek}`, and not its short name, as
  `\p{Grek}`. The properties of emoji are not in `std/regex`.
  `\p{sc!=Greek}` is the complement of `\p{Greek}`, but ripgrep 14.1.0 reads
  it as `\p{Greek}`.

- **Pattern errors.** A pattern that does not compile gives exit status 2, as
  in ripgrep, but the message is that of `std/regex`.

- **Context and the buffers.** With context lines, ripgrep keeps the lines
  of the context in its buffer when it reads more. The count of these lines
  also depends on the last line that it wrote. The port keeps only the
  context lines. Thus, in a file with a NUL byte after the first 64 KiB,
  the buffer that holds it can start at a different offset.

- **Encodings.** ripgrep reads UTF-16 when a file starts with its byte order
  mark. The port does not.

- **Not in this slice.** `--json`, `-r`, `-U`, `-f`, `--column`, `-b`,
  `--vimgrep`, `--stats`, `--crlf`, `--null-data`, `--passthru`, `-M`,
  `--trim`, `--stop-on-nonmatch`, `-E`, `--pre` and `--colors`. The heading and the
  line numbers on a terminal are also not in this slice.

## Tests

- `rg_test.fern`: the options, the errors, the pattern, the context, the
  separators, the counts, the color and the binary files. It also tests the
  rules of an ignore file, the globs, the types and the precedence of the
  ignore files. And it tests `-m`, `--heading`, `--null` and the buffers
  for binary files. `TestSelfHostExampleRgUnit` runs it on x86-64, arm64 and
  wasm32-wasi with the leak census.

- `TestSelfHostExampleRgAgainstRipgrep` runs the port and `/usr/bin/rg`
  14.1.0 with 235 command lines on the same files, on x86-64 and on arm64.
  The stdout, the stderr and the exit status must be the same. The files
  include an empty file and a file with no last line terminator. They also
  include CRLF lines, a line of 209,008 bytes, UTF-8 text and four binary
  files. The cases include each option, a missing file, the standard input
  and the errors. 90 cases walk two trees with each kind of ignore file, a
  link, hidden files and a binary file of 70 KiB.

  The test fails when `/usr/bin/rg` is not ripgrep 14.1.0. A case with more
  than one file gives `-j1` or `--sort`. With more threads, ripgrep writes
  the files in the order that their searches end. Four cases use
  more threads and compare the lines in sorted order.

- `TestSelfHostExampleRgVendoredCases` runs the cases of the integration
  tests of ripgrep 14.1.1 (`testdata/ripgrep-tests.json`, from
  `tests/*.rs`) with the port and with `/usr/bin/rg`. Each case makes its
  files in a new directory, and each run gets `--path-separator / -j1`.
  `testdata/ripgrep-pass.txt` names the 221 of 306 cases that give the
  output of ripgrep. The test fails when a named case fails, and when a case
  that it does not name passes. Most of the other cases use options that
  are not in this slice. The licence is in
  `licenses/ripgrep/`.

## Performance

The goal of #15590 is a port that is faster than ripgrep. This section
records where the port is, and why.

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
session, on a host that other jobs also used.

| Command | ripgrep | Port | Ratio |
| --- | ---: | ---: | ---: |
| `rg 'Sherlock Holmes'` | 46 | 173 | 3.8 |
| `rg -c 'Sherlock Holmes'` | 47 | 163 | 3.5 |
| `rg -i 'sherlock holmes'` | 123 | 401 | 3.3 |
| `rg -ic 'sherlock holmes'` | 116 | 357 | 3.1 |
| `rg 'Sherlock Holmes\|...'` (five names) | 124 | 300 | 2.4 |
| `rg -c 'Sherlock Holmes\|...'` | 111 | 289 | 2.6 |
| `rg -c '\w+'` | 326 | 366 | 1.1 |
| `rg -co '\w+'` | 2,963 | 2,116 | 0.71 |

Each output is the same as that of ripgrep, but `-co '\w+'`, for the
reason above.

### Why the port is slower on literals

1. **The read of the file.** For `rg -c 'Sherlock Holmes'`, about 110 ms
   of the 163 ms is the input. `read_file_bytes` reads the file into the
   storage of the array with one `read`. `string_from_bytes_unchecked` takes
   over that array with no copy (#15723).

   ripgrep maps the file into memory and does not copy it. The fix that
   remains is in the runtime: map the file.

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

### A tree of files

Each command searches a checkout of this repository with its ignore rules:
26,272 files to search, in the page cache. The time is the best of 5 runs,
in milliseconds. The two programs ran one after the other in the same
session, on a host that other jobs also used. The output of the port is
the same as that of ripgrep, but for the order of the files with more than
one thread.

| Command | ripgrep | Port | Ratio |
| --- | ---: | ---: | ---: |
| `rg -j1 --files` | 57 | 51 | 0.9 |
| `rg -j4 --files` | 53 | 61 | 1.2 |
| `rg -j1 -c fn` | 266 | 431 | 1.6 |
| `rg -c fn` (4 threads) | 156 | 271 | 1.7 |
| `rg -j1 -ci sherlock` | 252 | 539 | 2.1 |
| `rg -j1 -n --sort path TODO` | 272 | 322 | 1.2 |

With one thread, the walk is faster than that of ripgrep. Before two changes to `std/glob`,
the walk took 816 ms, and 95% of the time was in the automaton of a glob.
Now the automaton stops at the first byte that leaves no state, and it
keeps its states in the bits of a u64.

In a search of the files, the port is slower because of the scan for a
candidate. Half of the instructions of `rg -j1 -c fn` are in
`Regex.next_candidate`. It finds each `f` with `__memchr`, and then it
compares the literal prefix. ripgrep looks for the rarest byte with SIMD
kernels. The string of each file takes over the array that
`read_file_bytes` returns, with no copy (#15723).

With 4 threads, ripgrep is 1.7 times as fast as with one, and the port is
1.6 times as fast. Each worker process of the port searches every fourth
file. The port walks the tree with one thread, and so `--files` is not
faster with more threads.
