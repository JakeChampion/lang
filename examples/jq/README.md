# jq

A port of [jq](https://jqlang.github.io/jq/) 1.7 to Fern (#15585). The port
implements the documented behaviour of the jq 1.7 manual. It does not copy
source code from jq.

```
fern -target x86-64-linux -o jq examples/jq/jq.fern
echo '{"a":[1,2]}' | ./jq -c '.a[] , {b: .a}'
```

## What it has

This is slice 1 of the port:

- Paths: `.`, `.a`, `."a-b"`, `.[e]`, `.[e:e]`, `.[]`, and the postfix `?`.
- The pipe `|` and the comma `,`.
- Literals: numbers, strings, `true`, `false` and `null`.
- Array construction `[e]` and object construction `{a, "b": e, (e): e, $x}`.
- The comparisons `== != < <= > >=`, `and`, `or`, and the unary minus.
- The command line flags `-c`, `-r`, `-n`, `-s`, `--arg NAME VALUE` and `--`.

The order of the outputs, the error messages, the line in `(at <stdin>:N)`
and the exit status are those of jq 1.7.

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

Arithmetic, `//`, the assignment operators, `..`, string interpolation,
formats such as `@base64`, `as` bindings, `if`, `reduce`, `foreach`, `try`,
`label`, `def`, and every builtin function. A program that uses one of them
stops with exit 3 and a message that ends in "is not in this port yet".
The next slices of #15585 add them.

## Tests

- `jq_test.fern` holds the unit cases of this slice. It runs on x86-64, arm64
  and wasm32-wasi with the leak census (`TestSelfHostExampleJqUnit`).
- `tests/jq.test` is the test suite of jq 1.7 (see
  `licenses/jq/NOTICE.txt`). `TestSelfHostExampleJqSuite` runs each case
  whose program is in this slice and compares the outputs as JSON values, as
  jq does. 56 cases pass, and each of the others is in a part of the language
  that the port does not have yet.
- `TestSelfHostExampleJqAgainstJq` runs the command line and the `jq` binary
  on the same arguments and input, and compares their stdout, stderr and exit
  status.
