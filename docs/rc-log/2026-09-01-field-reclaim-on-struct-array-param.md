# A struct reclaim helper handed a struct ARRAY

*2026-09-01* — self-host `irlower`; surfaced as #7948, the wasm-hosted compiler
refusing a program the native-hosted one compiled fine.

## The shape

```fern
function add(items: S[], n: string): S[] { return items.append(S { name: n }); }
function build(items: S[]): S[] { items = add(items, "a"); items = add(items, "b"); return items; }
```

`items` is a reassigned-and-returned parameter, so it is a SNAPSHOT param
(#3456 slice 2). The assign path then asked

```
var snty: string = s.struct_type_of_slot(slot);
if (s.struct_routes_field_reclaim(snty)) { return emit_field_reclaim_store(se, slot, snty, snsl); }
```

and got `snty = "S"`. But an ARRAY slot records its ELEMENT type in
`struct_type_of_slot`, so the slot holds an `S[]` BUFFER while the route
selected `__field_reclaim_S`, a helper written against an `S` BOX. Its field
offsets (`8 + k*8`) land on the buffer's elements, and past its end once
`k+1` exceeds the length: the helper then compared and `arr_dec`'d whatever
words followed a live buffer.

`struct_type_of_slot`'s own comment said `"" = not a struct`, which is what
made the wrong question look like the right one. It is now stated as what it
is — struct box OR struct-array buffer, with `is_arr_slot` the discriminator.

## Why only wasm fell over

Both backends emitted the same call: the IR is shared, and the reachable-and-
differing set between `-emit asm -target wasm32-wasi` and `-target x86-64-linux`
for the whole `wasm_ir_run` closure is 29 functions, of which exactly one
over-releases. `__field_reclaim_parser__StructDecl` walks **four** field slots on
wasm (offsets 16/24/32/72) and **one** on x86 (offset 16): the register backends
gate a `string[]` field on the whole-program `strfldok:arr:` / `arrbuf:`
admission, `emit_wasm_field_reclaim_body` releases every `frk == "a"` field
unconditionally. So the register backends read one word past a short buffer and
usually found nothing dec-able; wasm read four and found one.

The witness: `parser.inject_builtin_enums(mod.structs)`, reached from
`asmcore.check_undefined_calls`, threads a `StructDecl[]` through ~20
`structs = add_builtin_variant(structs, …)` rebinds. On a program declaring NO
structs the buffer is 0-2 elements long, so offsets 24/32/72 are all past its
end, and the `!= new && != snap` guards — comparing garbage against garbage —
let the dec through.

## Attribution notes (what actually worked)

- The `__fern_rc_underflow` counter stays **0**: the freed word was a live
  block at rc >= 1, so nothing under-flowed. `util.rc_underflow_guard` on every
  driver run is silent here, and so is a plain "does it over-release" check.
- What settled it: emit the SAME driver's asm for both targets from one
  `bin/fern-selfhost`, reduce each function to its sequence of rc helper calls,
  diff, and intersect the differing set with the call closure of the pass the
  corruption is observable after. 4,078 common functions → 699 differing → 29
  reachable → 1. Note the WAT's runtime-helper bodies are FOLDED s-expressions,
  so a line-anchored `^\s*call \$` scan reports them as call-free and inverts
  the sign of the diff; match `call \$` anywhere in the line.
- Dead ends that cost time and are worth not repeating: `callgate_expr`'s own rc
  placement is byte-for-byte identical on the two targets (47 calls, same
  order), a plain recursive borrow-walk over `ast.Expr` does not over-release,
  and a standalone program of the same shape compiles identically both ways.
  The defect was never in the walk — it was in a helper the walk's callee
  reached.
- The trigger's "two nested initialisers" is about the HEAP, not the AST: one
  frees a block nothing reuses before it is read, two supply the intervening
  allocation. Every passing row in #7948's bisection table folds to depth <= 1.

## The fix, and what is left

The route is gated on `!s.is_arr_slot(slot)`; a struct-array snapshot param
keeps `emit_snapshot_store`, the shallow guarded buffer release it already used
whenever the element type happened not to route field reclaim.

Pinned by `TestSelfHostWasmHostedCompilerMatchesNativeOnNestedArith` — the
wasm-hosted `wasm_ir_run` against the native-hosted one on nested arithmetic,
asserting identical exit code AND identical WAT, then running the module. The
native-hosted output is byte-identical before and after the fix.

The backend divergence that made this bite on wasm alone is measured but NOT
fixed here, and it is the next lead: `emit_wasm_field_reclaim_body` releases
every `frk == "a"` field, so a `string[]` field is released with no admission
where the register backends require `strfldok:arr:` / `strfldok:arrbuf:`. On a
struct-ARRAY buffer that was this bug; on a genuine struct BOX nothing here
witnesses it either way — the cow guard already skips a carried-over field, so
whether the missing admission can dangle a replaced one needs its own probe
against the leak matrices, since closing it removes releases the wasm leg's leak
gates may be relying on (#7648 added the deep half of this arm to fix a leak).
