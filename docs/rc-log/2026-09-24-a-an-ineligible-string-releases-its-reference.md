# An ineligible string local releases its reference

2026-09-24. Native. #10117.

```
function tag(src: string, i: i32): i32 {
    var x: string = src;
    var out: string = "";
    if (i % 2 == 0) { out = x; }
    return out.len() + i;
}
```

Called four times on a fresh string, `-sanitize` freed none of the four on
x86-64 and two on arm64.

## Cause

Two halves, one per frame.

- `out = x` retains, so `out` holds a counted reference. `out` is not
  freeEligible, and the exit sweep's string arm skipped every ineligible
  string, so nothing gave the reference back. Every other type falls
  through to the flat `__fern_rc_dec`, which never frees. That was safe to
  do for strings too, because a `string` local never holds a view (the
  checker refuses `str` into `string` with E003) and the sweep already
  skips the two uncounted kinds, moved locals and borrowed aliases. What
  was missing was the two-word form: on arm64 and wasm an inline string
  keeps its bytes in the data word, so a flat dec on `data` is unsafe.
- The caller's `line` stayed ineligible on x86-64 because
  `stringParamCounted` had no arm for the parameter as the VALUE of an
  assignment, so `tag` looked like it kept the string.

## Change

- The sweep releases an ineligible string without freeing it: the flat
  `__fern_rc_dec` on single-word x86-64, and a new `__fern_str_rc_dec(data,
  len)` on arm64 and wasm, which returns at once for an inline string and
  hands anything else to `__fern_rc_dec`.
- `stringParamCounted` credits `local = p`: the Assign lowering retains an
  ident source unless it is a move, and a frame-bound alias is never moved.

## Measured

- The issue's program: 4 of 4 blocks freed on x86-64, arm64 and wasm,
  from 0 / 2 / 2. The self-host's x86-64 build already freed 4 of 4.
- `TestConformanceLeakCensusX86_64`: `http_content_type` 5 → 0 unpaired,
  `http_cookies` 8 → 7.
- `TestIneligibleStringLocalReleasesItsReference` pins four shapes on all
  three targets. Seven of the twelve legs fail without the change.
