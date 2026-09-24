# What strict mode found

2026-09-24. Self-host typed path.

`FERN_SEM_IR_STRICT=1` turns the silent fallback to the AST lowering into
exit 3. Run over e2eselfhost shards 0–2 of 12, `conformance/cases/`,
`examples/` and `coreutils/`, it found three shapes the typed path refused in
programs every checker accepts. Each module then ran on the AST lowering,
which answered correctly, so no test noticed.

## 1. A variant literal's float payload

```
function f(): Option[f64] { var y: f64 = Some(3.14)?; return Some(y); }
```

`f64_tryop_widen` in the conformance corpus. `settled()` settled a bare
unsuffixed float to `f64` but left one inside a union's type arguments
polymorphic, so `Some(3.14)` stayed `Option[<float>]` and matched no
destination. `settled()` now settles a union's arguments, and `variant_call`
settles the whole literal before it is compared.

## 2. An unannotated map literal

```
var m = Map { true: 5, false: 9 };
```

`TestSelfHostNarrowMapKeyAnswersX86_64`. The literal desugars to
`map_new(n).insert(k, v)…` (`map_new_i32` for integer keys). The self-host
checker typed only `map_new`'s result as a map, and neither took its columns
from the entries.
The map had no shape, so the construction was refused.

- **Checker.** Each `.insert` the literal desugars to names the columns it
  still lacks, as native types the literal. The parser builds those calls
  with no source position. A written `map_new(n).insert(…)` keeps its columns
  unresolved, which is what native does with it (#10214 is the self-host
  accepting that chain).
- **Typed path.** `method_call` takes an insert receiver's shape from the
  checker when the destination names none.

## 3. A match in value position at a reference type

```
var p: P = match (t) { (a, b) => P { x: a } };
```

`TestSelfHostMatchExprValueLocalWasm`. The parser routes the arms through a
local it declares as `var $v = 0` before any arm names a type. At a struct,
union, array or tuple type that `0` is an `i32` flowing into a reference
slot, and the typed path refused it. A placeholder local initialised with a
bare `0` at a non-integer type is now bound to `ssasem.zero()`, the null
word. It holds no unit, and every arm's store replaces it.

## Measured

- The six `TestSelfHostMatchExprValueLocal` programs (array, option, result,
  struct, nested if-arms, tuple) produce whole under strict. Under
  `FERN_LEAKCHECK=1` each is balanced (allocs = frees, `live_bytes=0`) and
  matches the interpreter.
- `TestSelfHostSemanticProduction` gains three rows, one per shape, on
  x86-64, arm64 (`-sanitize` on x86-64) and wasm:
  - `a-variant-literal-settles-its-float-payload`
  - `an-unannotated-map-literal-names-its-columns`
  - `a-match-value-local-starts-at-its-types-zero`

  The first fails without its fix.
- `TestSelfHostCheckerCodesX86_64` gains `maplit-bool-keys-ok` and
  `maplit-value-type-from-entries`. The E003 row fails against main's checker,
  which accepted a string-valued literal read into an `i32`.
- `TestSelfHostSemIRStrict` pins the flag: exit 0 on a produced module, exit 3
  naming the refusal on a refused one, and the AST lowering's compile without
  the flag.

## Next lead

The strict census still refuses:

- raw-floor intrinsic calls in two tests (`__rc_dec`, `__raw_data`);
- an array of views (#10215).

Every runtime helper also still compiles through the AST lowering
(`SELFHOST-SEMANTIC-SOURCE.md`, "Retiring the AST lowering").
