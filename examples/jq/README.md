# jq

A port of [jq](https://jqlang.github.io/jq/) 1.7 to Fern (#15585). The port
implements the documented behaviour of the jq 1.7 manual. It does not copy
source code from jq.

```
fern -target x86-64-linux -o jq examples/jq/jq.fern
echo '{"a":[1,2]}' | ./jq -c '.a[] , {b: .a}'
```

## What it has

The port has these parts of the language:

- Paths: `.`, `.a`, `."a-b"`, `.[e]`, `.[e:e]` and `.[]`.
- The postfix `?`. After a step of a path it guards that step only, as in
  jq: `.a.b?` is an error when `.a` is an error. After each other term it is
  `try`.
- The pipe `|` and the comma `,`.
- Literals: numbers, strings, `true`, `false` and `null`.
- Array construction `[e]` and object construction `{a, "b": e, (e): e, $x}`.
- The comparisons `== != < <= > >=`, `and`, `or`, and the unary minus.
- The arithmetic operators `+ - * / %` on each type that jq defines them for.
- The alternative operator `//`.
- `if`, with `elif` and `else`.
- The binding `e as $x | e`, and a destructuring pattern in the place of
  the variable: `. as [$a, {b: $c}]`
- The operator `?//`, which gives the next pattern after an error
- `reduce` and `foreach`, with a variable or a pattern.
- `def`, with filter parameters and `$` parameters. A function can call
  itself.
- `try`, with `catch`. An error holds a JSON value, as in jq.
- `..`, which is `recurse`.
- String interpolation, in a literal, in an object key and after a dot.
- The update operators `=`, `|=`, `+=`, `-=`, `*=`, `/=`, `%=` and `//=`.
- The formats `@text`, `@json`, `@html`, `@uri`, `@csv`, `@tsv`, `@sh`,
  `@base64` and `@base64d`, alone and before a string.
- An array as an index: `.[[1, 2]]` gives each place of that part.
- `label $name | body`, and `break $name`, which stops the body
- `$__loc__`, which gives the file and the line of the program

The port has each function of jq 1.7, with the definition of jq or with
the same behaviour:

- The regular expressions: `test`, `match`, `capture`, `scan`, `splits`,
  `split/2`, `sub` and `gsub`, with the syntax of Oniguruma (see
  "Regular expressions").
- Each math function of jq, on the functions of `std/f64` (see "Numbers").
- `input`, `inputs`, `input_line_number`, `input_filename`, `$__loc__`,
  `halt`, `halt_error`, `stderr`, `debug`, `env`, `$ENV`, `$ARGS` and
  `builtins`.
- The stream functions `tostream`, `fromstream` and `truncate_stream`.
- `limit`, `first`, `until`, `while`, `repeat`, `range` and `recurse`, which
  stop at the first output that the caller does not need.

The port has the modules of jq 1.7: `import`, `include`, data imports,
`modulemeta`, `get_search_list`, the `-L` directories and the include of
`~/.jq`. As jq does, the port gives C `realpath` the path of a module, of a
`-L` directory and of the program file. Thus each symbolic link is followed.
When `realpath` fails, the path stays as it is. A WASI program has no
`getcwd`, so the port takes the current directory from `$PWD`.

The command line has each option of jq 1.7, with the messages and the exit
codes of jq, except the debug options. `--debug-dump-disasm` and
`--debug-trace` show the bytecode of jq, and the port has no bytecode.

`--run-tests` is the test runner of jq. It reads the test file, or stdin,
with `--skip N` and `--take N`, and writes each line of jq. As in jq, a
test has no input callback, so `debug`, `stderr` and `halt_error` write
nothing, and `get_prog_origin` and `get_jq_origin` give no output.

## Generators

A filter gives each output to a callback. The callback takes the state of
the run and gives it back, so the state changes in place. A generator stops
at the first signal: an error, a `break` or a halt. Thus a generator
with no end stops in `first`, `limit`, `label` and `isempty`, as in jq.

A filter is single when each run of it gives one output at most and does
nothing after that output. The port finds the single filters, with each
function of a `def`. It takes the value of a single filter first in these
places: the right side of an operator, the value of an object entry, a
condition, the source of `as`, and a `$` argument. Then the frames of that
filter are gone while the rest runs. The order of the outputs, the errors and
the effects stays that of jq, because a single filter does nothing after its
output.

`try` catches a `break` of its body as the error `{"__jq": N}`. The label
numbers are those of jq, because the functions of the port that jq defines
with a label take a number as jq does.

jq gives an error for a path step after a destructuring pattern in `path`.
The port gives the path.

## Input

The input is a source that reads one value at a time. When the result is
sure to be that of jq, it reads a value with `std/json.parse_at`. Otherwise
it reads with `reader.fern`, the reader of jq 1.7. The source reads a file
when the program gets to it, so `jq -n` does not read stdin.

- jq reads more than JSON. A number can be `nan`, `infinity`, `1.`, `.5` or
  `+1`, and the values of a stream need no separator between them.
- A value at the top level is complete at its last byte. So `{"a":1}}` gives
  the object, and then the error for the second `}`.
- A sequence of bytes that is not UTF-8 is U+FFFD in a string.
- A parse error stops the command line with `jq: parse error:`, the message,
  and exit 5.

The files and the places of the input:

- jq reads its files as one text. A value or a raw line can go on in the
  next file. The line of a parse error counts from the start of the first
  file. A byte order mark counts only before the first byte of the input,
  and raw input keeps it.
- After the end of the input, the place of an error is the last file and
  its last line until the next read, when the last read of jq gave no value.
  Then it is `<unknown>`.

`--seq` reads as jq 1.7.1 does. It drops the text before the first record
separator, and it gives an error for a number or a value that a separator
cuts. After an error, it reads on at the next byte, although the message
says "need RS to resync". It prints each parse error with
`jq: ignoring parse error:`.

## Recursion

jq makes a tail call with no new stack frame. The port uses a stack frame for
each call of a function of a `def`. Thus the stack sets the limit of the
depth of a function that calls itself. A depth of 1000000 is good on x86-64.

jq keeps its frames on the heap, so only the memory sets its limit. A depth
of 3000000 is good in jq, and a depth of 30000000 gives an error that jq
cannot allocate memory. In the port, a deeper recursion stops with a stack
overflow and exit 134, because the evaluator calls itself for each filter.

On wasm32-wasi, wasmtime gives 512 KiB of stack, and a depth of 100 is good.
The compiler makes different frames for different programs, so the limit is
between 150 and 500. The option `-W max-wasm-stack` of wasmtime makes the
stack larger.

## Numbers

jq 1.7 writes a number in one of two forms, and the port does the same:

- A number literal of the program or of the input keeps its digits. jq writes
  it as the decNumber library does: `1.000`, `1E+2`, and `123.0` for `12.30e1`.
- A computed number is an f64. jq writes its shortest digits, as
  `jvp_dtoa_fmt` does: `1e+17`, `1e-07`, `0.0001`.
- jq writes NaN as `null`, and an infinity as the largest f64 of its sign.
  The two stay numbers: NaN is below each number in the order, and it is not
  equal to NaN.

Two literals compare as exact decimals, so
`13911860366432393 == 13911860366432392` is false, as it is in jq 1.7.1.
The other pairs compare as two f64 values. To get the f64 of a literal, jq
rounds it to 17 significant digits, half to even, and the port does the
same. The port starts the text of a computed number with `~`, so it knows
the two kinds apart.

A literal has the range of the decNumber context of jq. When its adjusted
exponent is more than 999999999, it is an infinity. An exponent below
-1147483646 rounds half up to that exponent, as `0E-1147483646`.

jq takes the result of a math function from the C library. The port takes
it from `std/f64`. `cbrt`, `hypot`, `erf`, `erfc`, `lgamma`, `tgamma`, the
Bessel functions, `sin` and `cos` of `std/f64` follow glibc 2.39 for x86-64.
A result can still differ from jq in its last bit, for two reasons:

- glibc on a CPU with FMA computes `sin`, `cos`, `exp`, `log`, `pow` and
  some other functions with fused multiply-adds. `std/f64` has no fused
  multiply-add, so it cannot give the same roundings. For `sin` and `cos`,
  about 7 arguments of each 10000 differ. Each function that calls one of
  these, such as the Bessel functions for x >= 2, can differ too.
- `tan`, `asin`, `acos`, `atan` and `atan2` do not follow the algorithms of
  glibc yet, and differ in the last bit more often.

The results also differ from one C library to the next, so jq on a
different system can give different last digits. The exact results are the
same: `exp10` of 2 is 100.

The functions with exact results, such as `frexp`, `fmod` and `nextafter`,
give the results of glibc. The jq 1.7 binary of Ubuntu has no `pow10`,
because glibc 2.39 has none, and the port gives its error.

## Dates

`dates.fern` has the calendar, `strftime` and `strptime`. The results are
those of jq 1.7 with glibc and the time zone UTC.

- The time zone is always UTC. `localtime` is `gmtime`, and `strflocaltime`
  is `strftime`. `%Z` gives `UTC` and `%z` gives `+0000`.
- `strftime` has the conversions of C for the C locale. It has no flags and
  no widths: a conversion that it does not have stays as it is in the format.
- `strptime` gives 8 for the day of the week and 367 for the day of the year
  when the text has no date, as jq does with glibc.
- `mktime` gives an error for the results -1 and -2, as jq does.

## Regular expressions

`regex.fern` is an engine for the syntax of Oniguruma for Perl, which jq
1.7 uses. A pattern compiles to a program that a backtracking machine
runs, so the matches and the captures are those of jq. The engine has the
classes of Unicode, the POSIX brackets, `\p`, lookahead, lookbehind, atomic
groups, possessive repeats and back references. An error has the message of
Oniguruma after "Regex failure:".

`i` uses the simple case folding of Unicode. Oniguruma also folds one
character to two, as `ß` to `ss`. The port does not.

## Compile errors

A compile error names its line, and the line and the spaces up to the error
follow, as in jq. The port reports each undefined function and variable.
A syntax error has the names of the tokens of bison, as jq does. The parser
of jq is a parser of bison. The port names the expected tokens only in the
places where jq names them most often. In other places, the text after
"expecting" can differ.

## Tests

- `jq_test.fern` holds the unit cases. It runs on x86-64, arm64
  and wasm32-wasi with the leak census (`TestSelfHostExampleJqUnit`).
- `tests/jq.test`, `tests/onig.test`, `tests/manonig.test` and
  `tests/man.test` are the test suites of jq 1.7 (see
  `licenses/jq/NOTICE.txt`), and `tests/modules` holds the modules that
  jq.test imports. `TestSelfHostExampleJqSuite` runs each case and compares
  the outputs as JSON values, as jq does. Each of the 709 cases passes.
- `TestSelfHostExampleJqAgainstJq` runs the command line and the `jq` binary
  on the same arguments and input, and compares their stdout, stderr and exit
  status. It runs `--run-tests` on each test file of jq, and it follows
  symbolic links in module paths.

## Speed

The port is faster than jq 1.7 on each filter of #15585. These are the best
of three runs on a host with 4 cores, in seconds of wall time:

| Filter | jq, 100 MB | port, 100 MB | jq, 100,000 documents | port, 100,000 documents |
| --- | --- | --- | --- | --- |
| `.` | 5.02 | 2.73 | 0.38 | 0.23 |
| `.[] \| .name` | 3.74 | 1.80 | 0.24 | 0.11 |
| `map(select(.x > 1))` | 5.43 | 2.85 | 0.38 | 0.19 |
| `group_by(.k)` | 9.13 | 5.60 | | |
| `[paths]` | 17.74 | 5.27 | 1.63 | 0.25 |

On the documents, the filters are `.name` and `select(.x > 1)`.
