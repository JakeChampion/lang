# A rebound fresh-ret-call struct local earned no credit at all

*2026-09-04* — `collect_fresh_ret_call_names`' blunt `body_assign_targets`
exclusion, the hole it left between two paths, and why the generated leak matrix
is structurally unable to see it.

## The shape

```fern
struct S { name: string, n: i32 }
function mk(i: i32): S { return S { name: w("k"), n: i }; }

function round(i: i32): i32 {
    var s: S = mk(i);      // producer-call init
    s = mk(i + 1);         // …then rebound
    return (s.name.len() + s.n) % 101;
}
```

800 allocs / **0** frees, 41 600 live over 200 rounds, against native's 800/800.
Not one dec of any kind is emitted in `round` — neither the rebind's release of
the orphaned box nor the exit sweep's release of the final one.

## The INIT spelling decided it, not the rebind

The four-way measurement is the whole finding. Only the left column moves:

| init | rebind | decs in `round` | self-host |
|---|---|---|---|
| `S { … }` | `S { … }` | 3 | 800/800 |
| `S { … }` | `mk(i)` | 3 | 800/800 |
| **`mk(i)`** | `S { … }` | **0** | **800/0** |
| **`mk(i)`** | `mk(i)` | **0** | **800/0** |

`collect_fresh_struct_names` (literal binds) never carried a reassignment
exclusion; `collect_fresh_ret_call_names` (producer-call binds) dropped every
name in `body_assign_targets` before it could reach the shared gate. So the two
spellings of one program diverged on a filter, not on a hazard.

The leaked memory is not confined to rc fields, which is what makes the shape
ordinary rather than exotic. A struct with **no rc fields at all** leaks its box
just the same — measured on `struct N { a: i32, n: i32 }`, 800/400, where the
freed half is the producer's own internal string and the leaked half is two
boxes a round.

## The hole was between two paths, and the comment named the wrong one

The exclusion deferred to the snapshot-LOCAL path: *"a reassigned fresh-ret
local is the snapshot-LOCAL path's job (it move-rebinds + moves out via
`return`)"*. That path (`reclaim_snapshot_locals`) claims exactly the locals
that are threaded and **moved out**. A rebound local that simply goes dead is
neither read-then-dead-and-never-reassigned (excluded here) nor moved out
(not claimed there), so it fell between and earned nothing.

## Why removing the filter is safe

Both collectors feed one gate. `reclaimable_fresh_struct` already validates each
rebind — `reassigned_from_alias` refuses a rebind that leaves a BORROW in the
slot, `recv_borrow_escapes_via_result` the handed-back receiver — and
`body_unsafe_for` still rejects a local that is returned, stored, or passed to a
non-borrowable param. The literal-bound sibling has been relying on exactly that
for as long as it has existed, which is the evidence the gate is load-bearing
rather than incidental: an alias rebind (`s = o`) off a literal init measures
clean *and* answers correctly today.

`#6535` retired the same blunt exclusion for the ARRSTRUCT family, validating
each rebind individually rather than sinking the credit on sight.

## Measured

Native x86-64, `bin/fern -interp` and the self-host agree on every exit; each row
re-run under `FERN_SANITIZE=1`, which is what reports an over-release rather than
a leak. `__rc_underflow_count()` is 0 throughout.

| probe | native | before | after |
|---|---|---|---|
| call init, call rebind | 800/800 | 800/**0** | 800/800 |
| call init, literal rebind | 800/800 | 800/**0** | 800/800 |
| literal init (control) | 800/800 | 800/800 | unchanged |
| scalar-only struct, rebound | 800/800 | 800/**400** | 800/800 |
| `string[]` field, rebound | 2000/2000 | 1600/**400** | 2000/2000 |
| 3 generations in a loop | 2000/2000 | 2000/2000 | unchanged |
| old value appended to a live `S[]` | 1600/1600 | 1600/800 | unchanged, refused |
| a field carried out before the rebind | 1200/1200 | 1200/1000 | unchanged, refused |
| rebound then returned | 1200/1200 | 1200/800 | unchanged, refused |

The last three are the escape shapes, and each is a wrong-ANSWER probe as well
as a census row: it reads its held string back after `churn` has had the chance
to recycle anything freed early, and answers 100 on a short read. All three keep
their leak, which is the direction a refusal must fail in.

## Trap — the leak matrix cannot see this class

`selfhost-leak-matrix.txt` is 134 rows, all `clean clean`, and it has a `rebind`
scope. It still gave this shape no coverage, because **every kind in the
generator inits from a literal**:

```go
init: "var x: P = P { xs: [i, i + 1], k: i };", init2: "x = P { xs: [i + 2], k: i + 1 };"
```

The grid's axes are kind × scope × consumption × origin, and the producer-call
init is not one of the origins. So "the matrices are exhausted as a gap source"
holds only for the spellings they generate — adding a row would not have caught
this, and the gate lives in
`internal/e2eselfhost/self_host_fresh_ret_rebind_test.go` instead, where the
literal-init control sits beside the call-init rows to localise a regression to
the collector rather than to the shared gate under it.

## Gates

- `TestSelfHostFreshRetRebind{X86_64,SanitizeX86_64,WasmIR,IRArm64}` — 9 cases × 4 legs
- `TestSelfHostLeakMatrixX86_64` — green, pin file unmoved
- `TestSelfHostRcPlanDiff` — green, no table moved
- `TestSelfHostStrFieldReadShare*`, `TestSelfHostStrArrField*`,
  `TestSelfHostStructStrFieldReclaim*`, `TestSelfHostStrfldDropGate*`,
  `TestSelfHostAliasedParamBorrow*`, `TestSelfHostUnionPayloadReclaim*`,
  `TestSelfHostOptstructReclaim*`
- `TestSelfHostPerModuleEmitAllFixpointX86_64`
- the complexity ratchet, `make check-sources`

## Next lead

Two residues, both measured here and both left open:

1. `var t = s.name` with no rebind still leaks one block a round (400/200) — the
   read-side retain fires and no credit family ever releases `t`. That is
   #5338's first tractable slice, untouched by this change; the `A` row
   (bind + rebind) improved 800/0 → 800/600 and the remaining 200 is exactly it.
2. A plain `string` local rebound from a producer call — `var x = mkstr("x");
   x = mkstr("yz");` — measures **native x86-64 400/200, self-host 400/400**:
   the reverse direction, a native-side leak rather than a port gap. It holds
   whether or not the first value is read before the rebind. Worth a native look
   because the matrix's own `str__rebind__read` cell reports `clean clean` on
   x86-64, so that generated probe and this hand-written one differ somewhere
   that matters, and the cell is not covering what its name suggests.
