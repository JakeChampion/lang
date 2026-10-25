# JSON and protobuf errors

Status: [reference]

`json.parse(text)` returns `Result[json.JsonValue, json.JsonError]`.
`json.decode[T](text)` returns `Result[T, json.JsonError]`.
The `FromJson` trait and its derived methods use the same error type.
The old `json_parse` and `json_parse_result` names no longer exist.

```fern
import "std/json" as js;

@derive(js.FromJson)
struct Config { retries: i32 }

function main(): i32 {
  match (js.decode[Config]("{\"retries\":3}")) {
    Ok(config) => { return config.retries - 3; },
    Err(e) => { eprint(e.to_string()); return 1; }
  }
}
```

A syntax error retains its byte `offset`, 1-based `line`, and 1-based `col`.
Typed decode preserves the same position and message that `parse` returns.
A shape error has `offset` -1, `line` 0, and `col` 0.
The DOM does not retain source positions for individual values.

Use `json.error(message)` to create an error in a custom `FromJson` implementation.
The field, index, and variant helpers add context to its message and preserve its position.
`JsonError.to_string()` includes the source position when one exists.

Integer decoding uses the numeric modules and accepts the complete signed and unsigned ranges.
An out-of-range integer returns a shape error.
Float decoding uses the parser for its destination width.

`protobuf.decode(bytes)` returns `Result[protobuf.Field[], protobuf.DecodeError]`.
The error has a `kind`, an absolute byte `offset`, and a `field` number.
An invalid tag has `field` 0.
Both public error types implement `error.Error` and `cmp.Display`.

| Protobuf error kind | Details |
| --- | --- |
| `UnexpectedEnd` | A varint ends before its final byte. The offset equals the input length. |
| `VarintOverflow` | A varint exceeds 64 bits. The offset identifies its tenth byte. |
| `InvalidFieldNumber(number)` | The tag has a field number outside 1 through 536870911. |
| `UnsupportedGroup(wire)` | The tag uses group wire type 3 or 4. |
| `InvalidWireType(wire)` | The tag uses wire type 6 or 7. |
| `LengthOutOfBounds(length, remaining)` | The length exceeds the bytes after its prefix. The offset identifies the prefix. |
| `FixedOutOfBounds(required, remaining)` | A fixed value lacks bytes. The offset identifies the value start. |

The decoder checks complete unsigned lengths before an index cast.
It accepts all 64 value bits, including ten-byte encodings of negative signed integers.
It preserves unknown fields and repeated fields in wire order.
