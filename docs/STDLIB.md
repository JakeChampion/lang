# Standard library guide

Status: [reference] library conventions and links to the generated API reference.

The [generated standard library reference](https://jakechampion.github.io/lang/stdlib/) is the API reference.
It includes every published module, its declarations, and its source documentation.
This guide covers conventions across modules.

The [literate source inventory](LITERATE-STDLIB.md) links to all 152 handwritten module documents.
Their prose describes module behavior and contracts. Named code chunks supply the implementation, and test fences contain executable examples.

To build a local reference from this checkout:

```sh
fern -doc -o build/stdlib-reference internal/stdlib/std
fern -doc -o build/stdlib-reference internal/stdlib/core
```

Correct an API description in the comment above its declaration.
Correct a module introduction in the file's first comment block.
Keep a blank line between that introduction and the code or declaration documentation below it.

## Imports and namespaces

`std/` contains application libraries such as text processing, codecs, files, networking, and time.
`core/` contains foundations such as comparison traits, maps, iterators, and big integers.
Both namespaces have public APIs for application code.

Module function names omit the module name prefix: use `hex.encode` and `uuid.v4`.
Constructors use `new`, `of`, or `from`.
Examples include `cli.new`, `log.new`, and `log.new_json`.
Constants use uppercase names and `pub const`, such as `uuid.NIL`, `log.INFO`, and `fuzz.DEFAULT_ITERATIONS`.

Import a module to use its functions and receiver methods.
A qualified call uses the final path segment, or an explicit `as` alias.
A receiver method uses the value's type to select an implementation.

```fern
import "std/array";
import "std/i32";

function main(): i32 {
  let values: i32[] = [1, 2, 3];
  let doubled: i32[] = values.map((n: i32): i32 => n * 2);
  print(doubled[2]);
  return 0;
}
```

The numeric import supplies the display contract used by `print` in this example.
Built-in types such as `Option` and `Result` need no import, but their library methods do.

## Values and collections

Arrays and maps have value semantics.
An update returns a new value, and another binding retains its previous value.
The runtime can reuse storage when the old value has no other owner.
Rebind the result when you want the update to replace the current value.

Map iteration has no insertion-order guarantee after deletion.
Use `std/ordmap` when key order is part of the result.
The generated reference describes each collection's iteration and update methods.

## Absence and errors

`Option[T]` represents a value that can be absent.
`Result[T, E]` represents success or an error value.
Import `std/option` or `std/result` for their combinators.

`std/error` defines the shared `error.Error` trait and its `message()` method.
A `dyn error.Error` can hold different concrete error types that implement that trait.
See [error propagation](TRY.md) for the `?` operator and its conversion rules.

## Text and bytes

A `string` contains valid UTF-8 text.
Use `u8[]` for owned bytes and `[u8]` for a byte view.
Check the reference for which representation a codec or I/O function accepts.
Use a checked UTF-8 conversion for external bytes before you treat them as text.

String byte offsets and Unicode character positions are different units.
The `std/utf8` and `std/unicode` modules describe code-point and text operations.

`std/parse.int64(text)` returns `Result[i64, parse.IntError]` for checked decimal conversion.
It accepts an optional sign and the full signed 64-bit range.
Errors distinguish empty input, an invalid digit offset, and overflow.

`std/binary` reads and appends unsigned 32-bit and 64-bit integers in network byte order.
Reads return `Result` and reject offsets outside the input view.

`std/png.encode_rgb8(width, height, pixels)` encodes consecutive RGB triples as a PNG image.
It checks dimensions and buffer length before allocation and returns a typed encoding error.

`encode_rgb8_with_options` accepts `png.Options` for filters and compression.
`png.options()` selects `Filter.Unfiltered` and `Compression.Balanced`, which preserve the original output.
Available filters are `Unfiltered`, `Sub`, `Up`, `Average`, `Paeth`, and `Adaptive`.
`Adaptive` selects a filter for each row by the sum of absolute signed residuals.
`Compression.Fast` uses a greedy compressor with one candidate per match.
All modes preserve the input pixels.

Filter and compression choices affect both execution time and file size.

```fern
let opts: png.Options = png.Options { ...png.options(), compression: png.Compression.Fast };
let image: Result[u8[], png.EncodeError] = png.encode_rgb8_with_options(width, height, pixels, opts);
```

`std/deflate.deflate_fast(bytes)` exposes the same compressor for raw RFC 1951 streams.
It retains the 32 KiB match window and omits the predecessor chain and lazy match search.
The existing `deflate` function retains its balanced compression policy.

## CSV and conversions

`csv.parse(text)` returns `Result[string[][], csv.ParseError]`.
`csv.parse_line(text)` returns `Result[string[], csv.ParseError]` for one record.
Quoted fields can contain commas, quotes, and newlines.
The document parser accepts LF, CRLF, and CR separators between records.
The single-record parser rejects separators outside quoted fields.

A CSV error contains a zero-based byte offset and a `ParseErrorKind`.
The kinds distinguish misplaced quotes, bytes after a closing quote, unclosed quotes, and unexpected record separators.
An unclosed quote reports the input length as its offset.
CSV errors implement `error.Error` and `cmp.Display`.
The parser permits different row widths and returns no partial result on error.

`convert.TryFrom[T, E]` and `convert.TryInto[T, E]` preserve the concrete error type `E` in their results.
Use an error type that implements `error.Error` and `cmp.Display`.
The conversion traits do not convert errors to strings.

## Byte encodings

`std/hex`, `std/base32`, and `std/base64` encode byte views as text.
Their `decode` functions return `Result[u8[], codec.DecodeError]`.
`base64.decode_url` checks the URL-safe alphabet.

A decode error contains the encoding name, a byte offset, and an error kind.
The error kinds identify invalid bytes, invalid lengths, invalid padding, and nonzero unused bits.
An incomplete final group reports the input length as its offset.
An invalid byte or nonzero unused bits report the position of the affected byte.
Decode errors implement `error.Error` and `cmp.Display`.

Hexadecimal text accepts either case and requires complete byte pairs.
Base32 uses uppercase letters and digits from 2 through 7.
Base32 and Base64 accept complete padded or unpadded input.
All decoders accept empty input and reject malformed input without a partial result.

```fern
import "std/hex";

function main(): i32 {
  match (hex.decode("4869")) {
    Ok(bytes) => { print(hex.encode(bytes)); },
    Err(problem) => { eprint(problem.message()); return 1; }
  }
  return 0;
}
```

`semver.parse` returns `Result[SemVer, semver.ParseError]`.
It checks identifier characters, empty identifiers, and numeric leading zeros.
Numeric core components must fit `i32`.
Numeric prerelease identifiers have no size limit.

`uuid.version` returns `Result[i32, uuid.ParseError]` after it checks the canonical UUID shape.
It accepts either hexadecimal case.
`uuid.v4` and `uuid.v7` create UUIDs, and `uuid.NIL` is the nil value.
Both parse error types provide a byte offset and implement `error.Error` and `cmp.Display`.

## Host capabilities

Some modules need capabilities that a target does not provide.
`fern -targets` lists the available target profiles.
A call that needs an unavailable capability produces a build diagnostic.
See [backend support](BACKEND-PARITY.md) for target formats and runtime coverage.
