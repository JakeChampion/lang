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
| `-f F` | `--file F` | The patterns of the lines of F, or of the standard input for `-`. |
| | `--pre P`, `--no-pre` | Search the output of the program P for each file. P gets the path of the file as its argument, and the file on its standard input. `std/process` runs it. |
| | `--pre-glob G` | Run the program of `--pre` only for the files that the globs keep, as with `-g`. |
| `-z` | `--search-zip` | Decompress each file with the extension of gzip, bzip2, xz, lzma, lz4, brotli, zstd or compress, and search the result. |
| `-a` | `--text` | Search binary files as text. |
| | `--binary` | Search a binary file that the walk finds as a file on the command line. |
| | `--mmap`, `--no-mmap` | Map the files into memory, or read them in buffers. This changes the rules for binary files. |
| `-j N` | `--threads N` | The count of workers. |
| `-E E` | `--encoding E`, `--no-encoding` | Decode the files from an encoding of the Encoding Standard, with `std/encoding`: each label of the standard, such as `latin1`, `sjis`, `euc-jp`, `gbk` or `koi8-r`, names one. `none` does not decode, and `auto` is the default: a file with a byte order mark of UTF-8 or UTF-16 is decoded. |
| | `--crlf` | A line ends with CRLF: `$` matches before `\r\n`, and the output writes `\r\n`. |
| | `--null-data` | A line ends with NUL, and the search is of text. |
| | `--stop-on-nonmatch` | Stop the search of a file at a line that is not selected after a selected line. |
| | `--no-unicode` | The pattern has the semantics of bytes and ASCII, as `(?-u)`. |
| `-U` | `--multiline`, `--multiline-dotall` | A match can hold line terminators, and the output writes each line of a match. `^` and `$` match at each line. With `--multiline-dotall`, `.` also matches a line terminator. |

### The output

| Option | Long form | Effect |
| --- | --- | --- |
| `-n`, `-N` | `--line-number`, `--no-line-number` | Line numbers on or off. |
| | `--column`, `--no-column` | The column of the first match, from 1. With `-o`, the column of each match. |
| `-b` | `--byte-offset` | The byte offset of each line, or with `-o` of each match. |
| | `--vimgrep` | Each match on a line of its own, with the file name, the line number, the column and the whole line. |
| `-r R` | `--replace R` | Write each match as R, with `$1`, `$name`, `${name}` and `$$`. |
| | `--trim` | Write no white space at the start of a line. |
| `-M N` | `--max-columns N`, `--max-columns-preview` | A line longer than N bytes is a note, or the first N characters and a note. |
| | `--passthru` | Write each line: the selected lines and the others as context. |
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
| | `--color never\|auto\|always\|ansi` | Color, with the default colors of ripgrep. `auto` writes color on a terminal with a TERM that is not `dumb`, and with no NO_COLOR. |
| | `--colors SPEC` | The color of the file names, the line numbers, the columns or the matches: `TYPE:fg:COLOR`, `TYPE:bg:COLOR`, `TYPE:style:STYLE` or `TYPE:none`. A color is a name, a number from 0 to 255, or three numbers with commas. |
| | `--hyperlink-format F`, `--hostname-bin P` | Each file name is a link of the format F, or of one of its aliases (`default`, `file`, `grep+`, `kitty`, `macvim`, `none`, `textmate`, `vscode`, `vscode-insiders`, `vscodium`). A link needs color. `{host}` is the output of P, or the host name of the system. |
| | `--field-match-separator S`, `--field-context-separator S` | The text after each field of a selected line and of a context line, with escapes. |
| | `--line-buffered`, `--block-buffered` | Accepted. The port writes its output in blocks of 64 KiB, and after each file on a terminal. |
| `-p` | `--pretty` | Color, a heading and line numbers. |
| | `--stats` | The counts and the times of the search, after the output. |
| | `--json` | Each line as a JSON message of ripgrep, with a message at the start and at the end of each file, and a summary. |

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
| | `--one-file-system` | Do not go down into a directory on another file system than the root of the walk. |
| | `--no-messages`, `--no-ignore-messages` | No error messages, or no messages about ignore files. |

### Other modes

| Option | Long form | Effect |
| --- | --- | --- |
| `-h` | `--help` | The short or the long help of ripgrep 14.1.0. |
| `-V` | `--version` | The version, or with `--version` the features too. |
| | `--pcre2-version` | The version of PCRE2 that `-P` follows. |
| | `--generate K` | The man page, or the completions of `complete-bash`, `complete-zsh`, `complete-fish` or `complete-powershell`. |
| | `--no-config` | Do not read the file that `RIPGREP_CONFIG_PATH` names. Else each line of that file that is not empty and does not start with `#` is an argument before those of the command line. |
| | `--debug`, `--trace` | Accepted. The port does not write the messages of the logs. |

Options and paths can come in any order. `--` ends the options. Short options
can share one dash, and a value can follow its letter, as in `-nA1`.

When the standard output is a terminal, the output has a heading and line
numbers, as in ripgrep. Else it has no heading, and no line numbers without
`-n`. These rules are the same as in ripgrep:

- A file name comes before each line when the search is of more than one
  file, or with `-H`. The standard input is `<stdin>`.

- A match line has `:` after its name and number, and a context line has
  `-`. A `--` line comes between groups of lines that are not adjacent, in a
  file and from file to file. With no context, there is no `--`. With
  `--heading`, an empty line comes between files.

- A last line without a line terminator gets one. A CRLF line keeps its CR.
  A byte order mark at the start of a file is not searched.

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
  ripgrep about filters. For a long flag that it does not know, it names the flags with
  similar names, as ripgrep does.

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
  Go's `unicode` package, and ripgrep 14.1.0 uses Unicode 15.0 through
  regex-syntax 0.8.2. A script takes its long name or its short name, as
  `\p{Greek}` and `\p{Grek}`, and the properties of emoji are there. As in
  ripgrep, `\p{sc!=Greek}` is `\p{Greek}`. Some properties are not in
  `std/regex`, and a pattern with one of them gives an error of
  `std/regex`. They are Script_Extensions, Age, Grapheme_Cluster_Break,
  Word_Break and Sentence_Break. The binary properties that are not in Go's
  tables are also not there, such as ID_Start, XID_Start and Bidi_Mirrored.

- **Pattern errors.** `regex.check` reports each error of regex-syntax
  0.8.2 with its text and its span, and ripgrep's hint about PCRE2. A
  pattern that regex-syntax accepts can go above a limit of `std/regex`:
  a count of a repetition above 1000, groups nested more than 128 deep, or
  a program of more than 100,000 instructions. Then the message is that of
  `std/regex`.

- **Context and the buffers.** With context lines, ripgrep keeps the lines
  of the context in its buffer when it reads more. The count of these lines
  also depends on the last line that it wrote. The port keeps only the
  context lines. Thus, in a file with a NUL byte after the first 64 KiB,
  the buffer that holds it can start at a different offset.

- **Times.** `--stats` and `--json` write times, and the times of two runs
  are not the same.

- **Logs.** `--debug` and `--trace` write no messages.

- **Decompression.** ripgrep 14.1.0 runs the programs `gzip`, `bzip2`,
  `xz`, `lz4`, `brotli`, `zstd` and `uncompress` for `-z`, and the port
  decompresses with `std/deflate`, `std/bzip2`, `std/xz`, `std/lz4`,
  `std/brotli`, `std/zstd` and `std/lzw`. When a program is not on the
  host, ripgrep searches the compressed file, but the port decompresses
  it. For data that is not valid, the port writes the messages of the
  programs. For data that ends early, the count of the bytes before the
  error can differ from that of the program.

- **PCRE2.** The port does not have PCRE2. It behaves as ripgrep 14.1.0
  that was built without the feature `pcre2`. `-P` and `--engine pcre2`
  give the message `PCRE2 is not available in this build of ripgrep.`
  `--engine auto` uses PCRE2 only after the default engine fails. And
  `--version` and `--pcre2-version` say that PCRE2 is not there. Thus the
  hint about PCRE2 after an error of backreferences or of look-around is
  not there.

- **Size limits.** `--dfa-size-limit` does not change the output of
  ripgrep, and the port accepts it. `--regex-size-limit` is not a limit of
  `std/regex`, and a pattern that ripgrep finds too large can compile.

## Tests

- `rg_test.fern` tests the options, the errors, the pattern, the context,
  the separators, the counts, the colors, the links and the binary files.
  It tests the ignore rules, the globs and the types. It tests `-m`,
  `--heading`, `--null`, `-r`, `--trim`, `-M`, `--passthru`, `-U`, the
  line terminators, the encodings and `--json`.
  `TestSelfHostExampleRgUnit` runs it on x86-64, arm64 and wasm32-wasi
  with the leak census.

- `TestSelfHostExampleRgAgainstRipgrep` runs the port and `/usr/bin/rg`
  14.1.0 with 521 command lines on the same files, on x86-64 and on arm64.
  The stdout, the stderr and the exit status must be the same. The files
  include an empty file and a file with no last line terminator. They also
  include CRLF lines, NUL lines, a line of 209,008 bytes, UTF-8 text,
  windows-1252 and UTF-16 text, pattern files and four binary files. The
  cases include each option, a missing file, the standard input and the
  errors. 90 cases walk two trees with each kind of ignore file, a link,
  hidden files and a binary file of 70 KiB.

  The cases of `-z` search files
  of gzip, bzip2, xz, lzma and compress, and files that are not valid. The
  cases of `--pre` run scripts that pass, that fail and that write no
  error.

- `TestSelfHostExampleRgWithoutPcre2` compares the port with the output of
  ripgrep 14.1.0 that was built without PCRE2: `-P`, `--engine pcre2`,
  `--engine auto`, `--pcre2-version` and `--version`. `/usr/bin/rg` has
  PCRE2, so the test holds the expected output.

  The test makes the times of `--stats` and `--json` the same before it
  compares them. 8 cases run with the standard output on a terminal, with
  and without a TERM.

  The test fails when `/usr/bin/rg` is not ripgrep 14.1.0. A case with more
  than one file gives `-j1` or `--sort`. With more threads, ripgrep writes
  the files in the order that their searches end. Four cases use
  more threads and compare the lines in sorted order.

- `TestSelfHostExampleRgVendoredCases` runs the cases of the integration
  tests of ripgrep 14.1.1 (`testdata/ripgrep-tests.json`, from
  `tests/*.rs`) with the port and with `/usr/bin/rg`. Each case makes its
  files in a new directory, and each run gets `--path-separator / -j1`.
  `testdata/ripgrep-pass.txt` names the 302 of 306 cases that give the
  output of ripgrep. The test fails when a named case fails, and when a case
  that it does not name passes. The four other cases compare the times of
  `--stats`. The licence is in
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

The counts of this table are the same as those of ripgrep, but for `\w+`
in `find` mode: 47,078,742 against 45,612,443, because `std/regex` knew only
ASCII word bytes. "Unicode" below gives the counts of ripgrep.

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

### Unicode

`std/regex` now matches characters, and `\w`, `\d`, `\s`, `\b` and `-i` are
Unicode. Its DFAs are lazy: a search adds a state when it first needs it.
`compile` fills each state of a small table at once, so that a search of a
short text does not pay for the lazy DFA.

The host had other jobs, so the tables below count instructions with
callgrind, which gives the same count on each run. "Main" is the port
before Unicode, with the same compiler.

Each count is in millions, for the 10 MB at the start of `big.txt`. It is
the count of the run less the count of a run on an empty file, so it
leaves out the compile.

| Pattern | Mode | Main | After |
| --- | --- | ---: | ---: |
| `Sherlock Holmes` | lines | 37.3 | 37.3 |
| | find | 4.4 | 4.4 |
| `(?i)sherlock holmes` | lines | 99.5 | 100.6 |
| | find | 76.6 | 74.2 |
| five names | lines | 74.2 | 74.2 |
| | find | 47.0 | 46.9 |
| `[a-zA-Z0-9_]+` | lines | 61.4 | 60.5 |
| | find | 713.3 | 708.9 |
| `\w+` | lines | 61.4 | 79.4 |
| | find | 713.3 | 834.4 |

| Command | Main | After |
| --- | ---: | ---: |
| `rg 'Sherlock Holmes'` | 9.9 | 10.1 |
| `rg -c 'Sherlock Holmes'` | 8.9 | 9.0 |
| `rg -i 'sherlock holmes'` | 86.9 | 84.3 |
| `rg -ic 'sherlock holmes'` | 84.1 | 80.9 |
| `rg 'Sherlock Holmes\|...'` (five names) | 60.0 | 61.3 |
| `rg -c 'Sherlock Holmes\|...'` | 55.3 | 55.6 |
| `rg -cw 'Holmes'` | 9.3 | 9.4 |
| `rg -c '\w+'` | 120.5 | 144.0 |
| `rg -co '[a-zA-Z0-9_]+'` | 1,423.7 | 1,470.2 |
| `rg -co '\w+'` | 1,423.7 | 1,770.8 |
| `rg -c '\p{Greek}+'` | error | 48.3 |

Each output of "after" is the same as that of ripgrep. On main, `\w` was
ASCII, `-co '\w+'` split words at each non-ASCII byte, and `\p{Greek}` was
an error.

The `\w+` rows stay above main. The class of Unicode has a program of 324
instructions, and its tables have 75,604 and 98,536 words. They cost 33 ms
to fill, and seconds in the interpreter, so `\w+` keeps the lazy DFA. Each
call of the lazy DFA runs about 50 more instructions than a call with a
full table. The ASCII class of main, `[a-zA-Z0-9_]+`, is not slower.

These rows also stay above main:

- The rows that write lines run about 2% more. The printer of the new
  options has more fields and more arguments for each line.
- `-co '[a-zA-Z0-9_]+'` runs 3% more. Of that, 22 million instructions
  are a copy of each line that the count of its matches makes.
- In the count loop, the code of the compiler adds 15 moves for each line
  after the call of `jump` (#15814).

The compile of `std/regex` costs more than on main for some patterns. A
full table for the five names costs 1.1 million instructions, about
0.4 ms.

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

### Literals inside a pattern

A pattern such as `\w+ing` has no literal prefix, and each word byte can
start a match. Thus each line was a candidate, and the port ran the DFA on
each line. Now `std/regex` finds the longest run of literal bytes that each
match holds, and `Regex.next_inner` finds the next copy of it. The line
loop goes to the line of that copy, and skips the lines before it. The
search for a literal prefix now starts from its least frequent byte, from
a table of the frequency of each byte in text.

The text is `corpus 50`: 52,428,815 bytes. Each command gets `-j1`. The
time is the best of 5 runs, in milliseconds, with the output to a pipe.
"Before" is the port without the two changes. The output of each command
is the same in the three programs.

| Command | ripgrep | Before | Port | Ratio |
| --- | ---: | ---: | ---: | ---: |
| `rg 'Sherlock Holmes'` | 24 | 32 | 32 | 1.3 |
| `rg -c 'Sherlock Holmes'` | 14 | 41 | 38 | 2.6 |
| `rg -i 'sherlock holmes'` | 26 | 110 | 120 | 4.6 |
| `rg 'Sherlock Holmes\|...'` (five names) | 28 | 64 | 62 | 2.2 |
| `rg -c '\w+'` | 140 | 107 | 109 | 0.8 |
| `rg -c '\w+ing'` | 23 | 586 | 43 | 1.9 |
| `rg -c '[a-z]+ Holmes'` | 20 | 265 | 42 | 2.1 |
| `rg -cw Holmes` | 14 | 29 | 28 | 2.1 |
| `rg -c xyzzy` | 12 | 35 | 35 | 3.0 |

`rg -c xyzzy` finds no line, and its time is the read of the file and one
scan. The causes above stay: the read copies the file, and the scans for
a set of bytes and for a pair of bytes do not use SIMD kernels. A literal
under `-i` is not an inner literal.

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
