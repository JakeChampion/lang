# A returned counted projection is not an escape (#8104)

The call-mode census (#8103) went looking for the shape #7792 was filed on
and found a population twelve times larger running the other way: **8,099
call sites over the self-host compiler where the caller performs a retain
for the call and the callee releases the parameter at its exit**, headed by
read-only parser peeks and AST walkers. `Par.peek_punct` had 338 of its 343
callers retaining first; `checker.check_expr` 207 of 214.

The cause is one branch of the escape analysis, and it is upstream of every
one of those sites.

## The shape

```fern
function (p: Par) peek(): lexer.Token {
    if (p.pos < p.toks.len()) { return p.toks[p.pos]; }
    return lexer.eof_tok(0, 0);
}
```

`paramEscapesInFn` calls `p` escaping, because `taintedReachesSlot`'s
`*ast.Index` case reports that a projection carries the parameter's sub-heap
out. An escaping parameter falls through the ownership ladder to
`paramVerdictOwned`, so every caller emits `__fern_rc_inc` before the call
and the callee's exit sweep runs an `is_unique`-gated deep drop — on a method
that reads one element and reclaims nothing. Measured on the shipped IR:

```
v4 = call "__fern_rc_inc", v1
v5 = call "__method_parser__Par_peek_punct", v4
```

with `ParamConsumed=[true]` and `__drop_struct_parser__Par` on every exit.

## Why the branch is wrong, and exactly how far

What it does not model is that the Return lowering incs every rc-tracked
alias on the way out (`needsRcIncOnAlias` matches `*ast.Ident` /
`*ast.FieldAccess` / `*ast.Index`). So the element that flows out carries a
unit of **its own**, and the parameter's box does not flow out at all —
`p.toks[p.pos]` is a different object. The caller's own reference is what
keeps `p` alive across the call either way, so the callee has nothing to
reclaim and nothing to transfer, which is the borrowed verdict.

`returnsOwnBox` already reads a returned projection exactly this way from
the other side — *"a different object the callee never owned"* — and this
reuses that file's own `returnedAliasIsRetained` for the counted half, so
the two sides cannot drift.

`returnedCountedProjection` is the exact inverse of the two branches it
overrides, **at the return sink only**:

| shape | before | after |
| --- | --- | --- |
| `return p.toks[p.pos]` | escapes | borrows |
| `return p.toks` | escapes | borrows |
| `return p` | escapes | escapes |
| `return Par { toks: p.toks, .. }` | escapes | escapes |
| `var q = p.toks; return q` | escapes | escapes |
| pair-form or TRMC callee | escapes | escapes |

The last three are the refusals the credit rests on. A bare parameter and a
fresh aggregate built around one are not projections. A projection bound to a
local first is refused because the credit is the inc the RETURN emits and
only a return is known to emit it — extending it to the taint propagation is
a separate argument nobody has made. Pair-form and TRMC are refused by name,
as `returnedAliasIsRetained` already does, because each rewrites the return
before the inc is reached.

## What it measures

**191 parameter positions over the self-host compiler flip from consumed to
borrowed, and 0 flip the other way** — the analysis only became more precise,
it never claimed a transfer it did not have before. Every one is a read-only
accessor: `Scope.lookup`, `Scope.lookup_method`, `Par.peek`, `Par.ident_at`,
`Par.keyword_at`, the `LowerState.*_of_slot` family, `checker.variant_payload_type_at`.
`Par.advance` (a fresh `Par` carrying `p`'s fields) and `irlower.lower_expr`
stay consumed, which is the boundary the table above describes.

Against #8104's own scoreboard, the `borrowed-variant:pair` class of
`ssa.CallModeSites`:

| | sites | callees |
| --- | --- | --- |
| before | 8,290 | 1,235 |
| after | **7,018** | 1,159 |

**1,272 witnessed retain/release pairs removed, 15.3%.** The solver and the
lowering moved together: their agreement rate reads 95.11% against 95.12%,
and the consumed/borrowed split moves 2,305/8,153 to 2,122/8,390.

Native-built self-host driver, x86-64:

| | |
| --- | --- |
| driver binary | 15,376,698 → **15,311,162 B** (−65,536, −0.43%) |
| its compiler output | md5-identical (`c4521192a24908521c62bf0e578f287d`) |
| driver leakcheck, small input | allocs 60,497 / frees 54,995 both sides, live 416,560 → 416,576 |
| driver leakcheck, a stdlib module | allocs 379,259 / frees 321,388, live 2,564,016 — identical |

The +16 B on the small input is deterministic across three runs and is the
"same alloc and free counts, unequal bytes" shape `docs/TEST-GATES.md`
describes: one differently-sized object survives, not an extra one. On the
larger input the number does not move at all. The five collection and
string benches emit **byte-identical** binaries, so nothing regressed there;
the shape is a compiler-shaped one and the driver is where it pays.

## Gates

- rc corpus leak gate, x86-64 and arm64: pass, every pinned case byte-identical.
- rc corpus correctness, all three backends: pass.
- conformance leak census: 467 fixtures, 82 leaking, 8,679 unpaired — identical to the pin.
- `ssa.Certify` against the runtime oracle: 0 findings over 1,481 functions.
- `internal/ir`, `internal/ssa`, `internal/interp`, `internal/checker`: pass.

`projection_accessor_receiver_borrows` is the new corpus case, and it watches
the drop this credit removes rather than the credit itself: `b` is the only
owner of its cells array, so if the returned element were not inc'd, the
round's exit sweep would free the buffer under the two Slots and the array
the caller still holds. Value-checked at 979, underflow counter folded in,
leak-gated at 0 on both backends.

## Trap

An `-emit asm` flag does not exist on `fern`, so a probe cannot count
emitted rc calls by reading assembly. Use `ssa.CallModeSites` (the census) or
`FERN_LEAKCHECK` on the built binary. And a probe of a call-boundary shape
measures nothing without `@noinline` on both the callee and its producer —
`ir.Inline` reaches every small callee otherwise.

## Still open

The taint-propagation half. `var t = p.toks[i]; return t` is the same object
flowing out through the same inc, and it still escapes. Closing it means
showing the inc fires for a returned local that only ever held a projection,
which is `freshLocalsIn`'s question one step further on, not this one.
