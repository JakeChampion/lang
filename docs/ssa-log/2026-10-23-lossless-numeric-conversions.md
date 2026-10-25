# Lossless numeric conversions

Issue: #12696.

The checkers admit scalar conversions that preserve every source value.
The integer conversions are `i32` to `i64`, `u8` to `u32`/`u64`/`i32`/`i64`,
and `u32` to `u64`/`i64`.
The float conversion is `f32` to `f64`.

The Go checker records source types before contextual settlement.
It then inserts casts in the checked AST.
The self-host checker inserts operand casts before interpreter evaluation.
Semantic lowering inserts casts before a scalar enters its destination.
A mixed `u32` and `i32` operation converts both operands to `i64`.

An expression retains its source arithmetic before its result converts.
An unsuffixed literal still takes the type that its context requires.
Stored containers retain their element types.
Fresh literals and constructors can convert each element before storage.
Character values remain distinct from integers.
Pointer arithmetic retains its existing rules.

The conformance case covers high unsigned bits, negative values, float precision,
constructors, closures, collection updates, and scalar value joins.
The negative cases cover narrower destinations, stored arrays, and float remainder.
`literal_local_read_at_two_widths` now checks a valid conversion after i32 arithmetic wraps.

Validation records are in
`docs/benchmarks/lossless-numeric-conversions-2026-10-23/`.
