# Strings, parsing, and format safety

Status: audit and scoped fix for [SEC-035](https://github.com/JakeChampion/lang/issues/13537).
Parent: [security epic](https://github.com/JakeChampion/lang/issues/13502).
The string representation decisions are in `docs/STR-VIEW-CONTRACT.md`, and the number parsers in `docs/NUMBER-PARSING.md`.

## What exists

| Surface | Rule |
|---|---|
| String bytes | A string is UTF-8 with byte indices. A view keeps its storage alive, and an update to the owner copies for the view. |
| UTF-8 at the boundary | `read_file` validates the file and answers `InvalidUtf8`, and `std/utf8.from_bytes` validates bytes and answers `None`. `string_from_bytes_unchecked` is the only way to hold invalid bytes, and its name says so. |
| Length | A string or array length is an `i32`, and a box holds at most 2^28 - 1 words, so a concatenation or a repeat past the ceiling aborts with the allocation size cause ([ARITHMETIC-AUDIT.md](ARITHMETIC-AUDIT.md)). |
| Number parsing | Every parser answers `OutOfRange` above its type's range, `InvalidDigit(offset)` for a bad byte, and `EmptyInput` for nothing. There is no trim and no digit separator. |
| JSON | The parser refuses a document nested deeper than 128 arrays or objects with a `JsonError`, so a hostile document cannot exhaust the stack. |
| Format strings | `f"..."` is compiled, so its placeholders are not data. `std/format.format` takes a specification at run time, and a width or precision now saturates at one million. |
| Fuzzing | `std/fuzz` drives a target with mutated seeds, and `fernsmith` generates whole programs (`docs/FUZZING.md`). |

## Change in this record

`std/format` parsed a width or a precision with `width * 10 + digit` and no bound.
A specification such as `{:2000000000}` made the format allocate two gigabytes of padding, and more digits wrapped the count.
A width or a precision now saturates at one million, which the module's contract states.

## Verification

The `std parse limits` suite runs natively on x86-64 and on arm64 under QEMU, and under the Go oracle.

- 128 nested arrays parse and 129 are refused, as are 100000.
- `utf8.from_bytes` refuses a truncated sequence, an overlong encoding, and a surrogate, and accepts a valid three-byte sequence.
- `parse_i32` refuses a value above the range and below it, and accepts the maximum.
- A two-billion width and a fourteen-digit width pad to one million bytes, an eleven-digit precision stays within that bound, and a small width is unchanged.
- Two fuzz runs of 2000 iterations each drive `json.parse` and `utf8.from_bytes` with mutated seeds. Every input must answer a result or an error, and a decoded string must keep its byte length.

## Remaining limits

- The iteration count and the seed set bound the fuzz runs. A coverage-guided run of the same targets is `go test -fuzz` territory in `docs/FUZZING.md`.
- The JSON depth limit is fixed at 128. A caller cannot lower it.
- A format width of one million still allocates one megabyte per placeholder. A caller that formats untrusted specifications bounds them itself.
- Other text parsers in the standard library have no depth or size limit of their own beyond the string ceiling. That includes CSV, headers, and the PEG engine.
