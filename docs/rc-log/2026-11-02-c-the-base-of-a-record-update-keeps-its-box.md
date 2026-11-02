# 2026-11-02 — the base of a record update keeps its box

`sempair.params_kept_apart`. Follows the `pr` lead in #8278.

## The shape

```fern
struct Line { buf: usize, pos: i32, pend: i32 }

function (l: Line) space(c: Cfg): Line {
  if (c.tabify) {
    return Line { ...l, pos: l.pos + 1, pend: l.pend + 1 };
  }
  buf_push_byte(l.buf, 32);
  return Line { ...l, pos: l.pos + 1 };
}
```

`space` reads its receiver apart and rebuilds it. `scalar_params` hands a
record of scalar fields to such a callee as its fields, so `space` took
three words and no box. Its result is kept whole by every caller, so the
return is a box, and each call built one. `put_sep` then released the box it
passed in. One allocation and one free per call, on every row `pr -m`
writes.

The pass kept a split parameter boxed when it was the base of a record
update and its mode was counted. The pass runs before the ownership
inference, so the mode it read was the declared one, borrowed. The rule
fired for a declared `own` alone. The inference then saw three scalar
parameters and no base, and had nothing to count.

## The rule

The base of a record update keeps its box, whatever its mode says at the
time. The inference counts every update base of a record type
(`ownership.escaping`), so the parameter ends counted. The update then
reuses its box in place when the caller's value dies at the call. A caller
whose value lives on pays one retain, as it does for any counted parameter.

## Measured

x86-64, `-O`, instructions from callgrind, `setarch x86_64 -R`.

| | before | after |
|---|--:|--:|
| `pr -m` of two 1,000,000-line files | 3,023,618,983 | 2,976,616,898 |
| allocations per row of `pr -m` | 13.5 | 12.5 |
| stage-2 compiler, bytes | 15,752,144 | 15,752,176 |
| stage 2 compiling `lexer.fern` to x86-64 asm, Ir | 1,723,204,292 | 1,723,615,580 |

The output of `pr -m` is the same. The compiler has one function with the
shape, `semlower.target_substitution`, whose receiver of four scalar fields
is now one counted box. The emitted assembly of `lexer.fern` is the same,
and the line-at-a-time utilities move by under 30 instructions each.

## Tests

`TestSelfHostUpdateBaseKeepsItsBox` runs a receiver with the shape above
through two thousand calls on every host target and wasm. The loop must
allocate nothing, and the leak check must balance. Without the change
the loop allocates one box per call.

`TestSelfHostScalarParams` keeps its `Line` case: the values are the same,
and its receiver is now a counted box.
