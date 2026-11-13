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

- Paths: `.`, `.a`, `."a-b"`, `.[e]`, `.[e:e]`, `.[]`, and the postfix `?`.
- The pipe `|` and the comma `,`.
- Literals: numbers, strings, `true`, `false` and `null`.
- Array construction `[e]` and object construction `{a, "b": e, (e): e, $x}`.
- The comparisons `== != < <= > >=`, `and`, `or`, and the unary minus.
- The arithmetic operators `+ - * / %` on each type that jq defines them for.
- The alternative operator `//`.
- `if`, with `elif` and `else`.
- The binding `e as $x | e`, with one variable.
- `reduce` and `foreach`.
- `try`, with `catch`. An error holds a JSON value, as in jq.
- `..`, which is `recurse`.
- The generators `empty`, `error`, `first`, `last`, `limit`, `range` and
  `recurse`.
- The array and object functions `add`, `all`, `any`, `flatten`,
  `from_entries`, `has`, `join`, `keys`, `keys_unsorted`, `length`, `map`,
  `max`, `min`, `reverse`, `sort`, `to_entries` and `unique`.
- The selectors `arrays`, `booleans`, `iterables`, `nulls`, `numbers`,
  `objects`, `scalars`, `select`, `strings` and `values`.
- The functions `not`, `tojson`, `tostring` and `type`.
- The command line flags `-c`, `-r`, `-n`, `-s`, `--arg NAME VALUE` and `--`.

The order of the outputs, the error messages, the line in `(at <stdin>:N)`
and the exit status are those of jq 1.7.

## Generators

The evaluator collects each output of a filter before it uses the outputs.
jq makes the outputs one at a time. The outputs and their order are the
same, and `first`, `limit`, `any` and `all` stop at the same output. But a
generator that has no end, such as `recurse(. + 1)` in a `limit`, does not
stop in the port.

## Numbers

jq 1.7 writes a number in one of two forms, and the port does the same:

- A number literal of the program or of the input keeps its digits. jq writes
  it as the decNumber library does: `1.000`, `1E+2`, and `123.0` for `12.30e1`.
- A computed number is an f64. jq writes its shortest digits, as
  `jvp_dtoa_fmt` does: `1e+17`, `1e-07`, `0.0001`.

Two numbers compare as exact decimals, so
`13911860366432393 == 13911860366432392` is false, as it is in jq 1.7. jq
compares a literal with a computed number as two f64 values. The port
compares them as decimals, so the two can differ when the literal has more
than 17 significant digits.

## What it does not have yet

- The assignment operators.
- String interpolation, and formats such as `@base64`.
- Destructuring patterns in a binding, and `?//`.
- `label` and `def`.
- Each function that is not in the lists above.

A program that uses one of them stops with exit 3 and a message that ends in
"is not in this port yet". The next slices of #15585 add them.

## Tests

- `jq_test.fern` holds the unit cases. It runs on x86-64, arm64
  and wasm32-wasi with the leak census (`TestSelfHostExampleJqUnit`).
- `tests/jq.test` is the test suite of jq 1.7 (see
  `licenses/jq/NOTICE.txt`). `TestSelfHostExampleJqSuite` runs each case
  whose program the port has and compares the outputs as JSON values, as
  jq does. 203 cases pass, and each of the others is in a part of the language
  that the port does not have yet.
- `TestSelfHostExampleJqAgainstJq` runs the command line and the `jq` binary
  on the same arguments and input, and compares their stdout, stderr and exit
  status.
