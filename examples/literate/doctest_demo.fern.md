# A literate document with runnable examples

This document defines a tiny library chunk and verifies it with embedded
`test` blocks. Run the examples with:

```sh
fern -doctest examples/literate/doctest_demo.fern.md
```

## The library

`clamp(x, lo, hi)` constrains a value to a range:

```fern
<<clamp>>=
pub function clamp(x: i32, lo: i32, hi: i32): i32 {
    if (x < lo) { return lo; }
    if (x > hi) { return hi; }
    return x;
}
```

## Examples

Each example pulls in `<<clamp>>` and returns 0 only when the behaviour
is correct — so these double as documentation *and* a test suite.

```fern test name=clamps-below-range
import "core/no_prelude";
<<clamp>>
function main(): i32 {
    if (clamp(0 - 5, 0, 10) != 0) { return 1; }
    return 0;
}
```

```fern test name=clamps-above-range
import "core/no_prelude";
<<clamp>>
function main(): i32 {
    if (clamp(99, 0, 10) != 10) { return 1; }
    return 0;
}
```

```fern test name=passes-through-in-range
import "core/no_prelude";
<<clamp>>
function main(): i32 {
    if (clamp(7, 0, 10) != 7) { return 1; }
    return 0;
}
```
