# A struct array from a producer call was reclaimed nowhere

```fern
struct P { s: string, n: i32 }
function mk(): P[] { var a: P[] = [P { s: w("p"), n: 1 }, P { s: w("q"), n: 2 }]; return a; }
function round(i: i32): i32 { var v: P[] = mk(); return v.len(); }
```

**1400 allocs / 200 frees, 32000 bytes live** over 200 rounds, against native's
600/600/0. 160 B per round, doubling with the round count. The byte-identical
literal init — the same two elements bound straight into `v` — was flat.

`frees` sat at exactly one seventh of `allocs` across an 8x range, which is what
said it was every round rather than a boundary: the credit was absent, not
mis-timed.

## There was no registry to widen

Every neighbouring element kind has a producer registry — `"ARRSTRUCTF:"` for
structs with an rc-array field, `"ARRTUPF:"` for tuples, `"ARENUMF:"` for enums,
`"AAC:"` for arr-of-arr. The scalar-field struct array had none, so
`collect_fresh_structarr_names` admitted an array literal and an append-built
local and nothing else. An uncredited binding is not refused loudly; it falls
through to the generic shallow buffer dec, which frees the outer buffer and no
element box.

So this is not the usual "the admission was too strict" shape. Both producer
forms leaked — the literal-returning one as much as the local-returning one —
which is the signature of a missing class rather than a narrow gate.

## The proof was already written

`fn_returns_fresh_arrstruct` and `body_has_nonfresh_arrstruct_return` ask exactly
the right question already: is every return either a fresh struct-literal array,
or a local built by self-append and handed back with no escape but the return?
Only `arrstruct_elem_struct_type` is specific to the deep class — it requires the
element struct to carry an rc-array field. `structarr_elem_struct_type` is its
complement, and `fn_returns_fresh_structarr` is the same four lines over it, so
the two registries partition the struct-element arrays instead of overlapping.

The append-built producer came along for free, which matters more than it looks:
that is how a producer that computes its elements has to be written, so a
literal-only registry would have left the common form leaking after the headline
shape was fixed.

## What stays refused, and why the exit codes are the assertion

Three shapes keep the old leak deliberately:

| shape | why |
|---|---|
| `a = a.append(e)`, `e` a bare ident | the container's counted co-owner is a local of the frame being left |
| `function passthru(a: P[]): P[] { return a; }` | the caller already owns what it binds |
| `P { s: nm }`, `nm` a parameter | the element's string is the caller's |

The third is admitted at the container level and declines at the field: the deep
element walk reaches the string under `__fern_rc_is_unique`, sees the caller's
reference, and leaves it. Verified by reading the string back after four
reclaims with two fresh strings allocated alongside — the shape that answers with
junk if the box went back.

A double free and a clean run report identical `allocs`/`frees`, so every row of
`self_host_structarr_producer_test.go` pins an exit code and only the rows that
must balance pin bytes. Each want was confirmed against native and `-interp`
before the self-host was run at all.

## Results

| shape | before | after | native |
|---|---|---|---|
| producer returns a local | 1400/200, 32000 | **1400/1400, 0** | 600/600, 0 |
| producer returns the literal | 1400/200, 32000 | **1400/1400, 0** | 600/600, 0 |
| append-built producer | 1600/400, 25600 | **1600/1600, 0** | 800/800, 0 |
| result read back beside a fresh array | 2800/1000 | **2800/2800, 0** | 1200/1200, 0 |
| same-named sibling alias of a param | 204/104 | **204/204, 0** | 102/102, 0 |
| literal init (control) | 1400/1400, 0 | 1400/1400, 0 | 600/600, 0 |
| rc-array-field element (ARRSTRUCT) | 1000/1000, 0 | 1000/1000, 0 | 1000/1000, 0 |

Flat at zero across 200 / 400 / 800 rounds, with the answers (68 / 53 / 23)
matching native's on every count.

The alloc ratio against native is #7351 — a heap string costs the self-host two
allocations — and is untouched by this. The divergence here was entirely on the
free side.

## Gate

`TestSelfHostStage2FixpointArm64` is the one that matters for a reclaim credit
(#7548 shipped one that segfaulted gen2 with every x86-64 gate green): 108 s,
green. Five rows of the new test fail on the parent commit; they are the x86-64
leg's, because a leak moves no exit code — which is why the wasm and arm64 legs
of a leak fix can only ever gate the over-release half.
