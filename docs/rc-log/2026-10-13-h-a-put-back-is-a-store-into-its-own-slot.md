# 2026-10-13 — a put-back is a store into its own slot

`compiler/rccertify.fern`: `put_back`, `put_backs`. Fixes the
`update-local-in-two-slots` case of `TestSelfHostSemanticReuseDifferentialX86_64`
(#12338), against 2026-10-13-d (the certifier walks under each tested reuse
token's value). No emitted byte changes. `rcmemo.version` goes to 7: the old
rule could certify a function that is short a retain, and that verdict may be
memoised.

## The shape

```fern
struct P { a: i32[], b: i32[], n: i32 }
function twice(own p: P): P {
    let x: i32[] = p.a;
    p = P { ...p, a: x, b: x, n: p.n + 1 };
    return p;
}
```

With the semantic inliner off, the update reads `a` off `p`, retains it once,
reuses `p`'s box where it is unique, and stores the field into slot `a` (where
it came from) and slot `b`. The certifier refused it:
`twice: leaked at return of v11 (borrow ld_at) in b12: 1 unit(s) still held`.

## The cause

Under the token holding the box, `put_backs` marks a borrow read off the box
as kept when a store puts it back into the slot it was read from, and
`apply_store` then spends nothing for a kept value stored into the box.
`put_back` checked the value and the box but not the slot, so the store into
`b` was taken for a put-back too. Neither store spent a unit, and the retain
showed as a leak. The same gap lets a body missing that retain certify.

## The fix

`put_back` holds only for a store at the offset the value was read from. A
store into another slot spends a unit as any store does. `put_backs` now
refuses a value with any store back into its own slot that fails the
conditions (a reload or a children-only drop between), which is what its
comment always said.

## Witnessed

`TestSelfHostRcCertifyCases` gains
`token-reuse-puts-back-and-stores-elsewhere` (the shape in twelve blocks; no
finding, and on main without the fix the same leak) and its `-unretained`
twin without the retain (`spent without a unit of v4`).
`update-local-in-two-slots` compiles and runs, and the compiler compiles
itself with the certifier on and the memo off.
