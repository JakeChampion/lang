# A `str` is not lent to a builtin that stores it

2026-09-24. Both checkers.

```
var out: string[] = [];
out = out.append(slice_unchecked(lower, start, i));
```

`examples/wasm/word_freq.fern` was one of the two programs under `examples/`
the typed path refused ("view element escapes its source",
`2026-09-23-f-the-example-programs-reach-the-typed-path.md`). Both checkers
accepted it.

## Cause

A `str` may reach a `string` parameter because a parameter borrows: the callee
never frees what it is lent (native `argAssignable`, self-host
`view_arg_borrow`). `append`, `with` and a map's `insert` are not borrows. Each
keeps its argument in the value it returns, so the view outlives the frame that
sliced it. Native carries the carve-out's own note, "owning sinks stay on the
strict assignable". The builtins' value positions were never marked as sinks.

## Change

- Native: `storesArgument` names the storing positions. The call check treats
  them as `own`, which turns the borrow off.
- Self-host: the `append` and `with` arms drop `view_arg_borrow`, and the Map
  arm checks `insert`'s key and value.
- Both report E038 with the `.to_owned()` hint.
- A `str[]` still takes a view. The self-host can tell the two arrays apart
  since #10201.
- `word_freq` copies with `.to_owned()`.

A sweep with both checkers of every `.fern` file in the repository and every
program embedded in a Go test found five that store a view:

- `word_freq`
- `internal/e2e/testdata/rc_nested_array_aliasing_bug.fern` (`split_lines`)
- `map_inline_string_kv_retain_no_crash` in `rc_correctness_test.go`
- `TestArm64MapGetMatchFullPipeline` (`tokenize`)
- `strview-escape-arg-safe` in `self_host_str_view_frame_ir_test.go`

The first four copy the view. The last is about a view passed to a callee that
keeps it, so its arrays are `str[]` now.

## Measured

- `word_freq` through the typed path: 96 of 96 declarations produced.
- `TestStrIntoAStoringBuiltinIsRefused` (native): four sinks refused and three
  controls accepted. All four refusals fail without `storesArgument`.
- `TestSelfHostCheckerCodesX86_64` gains the same four sinks and a `str[]`
  control.

## A `str[]` of literals on the typed path

`var many: str[] = ["", "ab", "cde"];` in `a-string-literal-is-already-a-view`
was produced only because the erasure read it as `string[]`. Typed as `str[]`
(#10201), it was refused twice: once as an array literal storing a view, and
once in `ssarc`, which had no physical type for an array of views.

- **semsource.** An array literal admits a view element that is a string
  literal. Its box is `.rodata` under the immortal rc, so it has no source
  to outlive.
- **ssarc.** `str[]` is supported. Every other view an array would store
  (`append`, `with`, a non-literal element) is still refused, so a `str[]`
  the typed path builds holds only immortal boxes. The array's drop
  decrements them, which does nothing.
- **Measured.** The row is back to 4 of 4 declarations and leak-free on
  x86-64, arm64 and wasm. A `str[]` literal rebuilt 50 times in a loop:
  `allocs=51 frees=51 live_bytes=0`, and it matches the interpreter.

An array of slice views is still refused (#10215).

## Next lead

`examples/cli/fold.fern` is the last example program the typed path refuses. It
merges views of two sources into one value.
