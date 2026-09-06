# An aliased struct's overridden array field leaks its buffer, once per rebind

*2026-09-06* — #8644, narrowed. The issue was filed as "one alias makes `append`
quadratic". The append is not the mechanism, and the defect is not quadratic.

## Minimal shape

No append, no `.with`. An alias, and a spread that overrides an rc array field:

```fern
struct S { ops: i32[], n: i32 }
function run(x: i32): i32 {
    var s: S = S { ops: [1, 2, 3], n: 0 };
    var i: i32 = 0;
    while (i < 200) {
        var prev: S = s;
        s = S { ...s, ops: [4, 5, 6], n: s.n + 1 };
        i = i + 1;
    }
    return s.n;
}
```

| | allocs | frees | live_bytes |
|---|--:|--:|--:|
| self-host | 40,200 | 20,200 | 960,000 |
| native | 20,200 | 20,200 | 0 |

## What leaks, established by scaling rather than assertion

Widening the literal from three elements to ten holds the leaked COUNT at 20,000
and moves `live_bytes` from 960,000 to 2,074,400 — 48 bytes each to 104. That is
24 bytes of header plus 8 per element (element slots are 8-byte on the register
backends), so the leaked block is the **array buffer**, one per rebind, and not
the struct box.

It is therefore CONSTANT per rebind. #8644's headline Θ(N²) — live_bytes
6,012,800 / 23,763,200 / 94,489,600 at N of 100 / 200 / 400 — was this flat
defect multiplied by an array that happened to be growing under `append`.

## Three-way isolation

| shape | self-host | native |
|---|---|---|
| alias + spread, scalar override only (`n: s.n + 1`) | 20,200 / 20,200 / **0** | 200 / 200 / 0 |
| alias + spread, array override (literal) | 40,200 / 20,200 / **960,000** | 20,200 / 20,200 / 0 |
| no alias + spread + append | 20,900 / 20,900 / **0** | 900 / 900 / 0 |

It needs an alias AND an rc array field being overridden. Either alone is clean.
That puts it in #8628's family — an overridden rc field's old value going
unreleased — rather than making it a separate accumulator problem, which matters
before the two are fixed as unrelated bugs.

The scalar-override row is what makes this actionable: same alias, same spread,
same rebind, clean. The only axis that moves is whether the overridden field
carries rc state.

## A lead, explicitly not a finding

`emit_self_overwrite_reuse`'s REUSE arm releases each overridden field's old
value per kind. Its shared/fresh arm releases the box and not the field. That is
where to look; it is not established that it is the cause, and no fix site
should be proposed from it without measuring first.

## Method note

Four structural inferences about this code were overturned in one session — the
`.with` fix site (a zero-byte diff), the `emit_self_overwrite_reuse` fix site (a
gate that exists to prevent exactly that release), a clause attribution that a
probe could not confirm, and the append lowering's identity. Each time, running
the compiler settled in one command what reading it had got confidently wrong.

The append lowering is the cleanest example. Three sites emit `ir.op_arr_push()`
and reading excluded all three, which cannot be true. Extending
`FERN_APPEND_REPORT` to expression-position appends (#8651) and adding one
temporary line per site named it in a single run: `lower_field_append_inplace`.
The exclusion had been wrong because the `__fern_arr_share_inc` / `_dec` bracket
belongs to its `.with` twin, not to it.

Prefer building the instrument. The two-emitter driver pair in this directory's
predecessor entry costs 30 seconds to rebuild and answers questions that
assembly-reading only appears to answer.
