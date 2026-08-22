# The struct alias, and the field that answers the wrong question

#7282's last limb. The reclaim side is a straight repeat of the container rule;
the prerequisite was not, and it is the only part worth reading twice.

## What it was

| shape | oracles | before | after |
| --- | --- | --- | --- |
| struct literal alias | `40` | `200/0` **8000** | `200/200` **0** |
| fresh-call alias | `23` | `200/0` **8000** | `200/200` **0** |
| conditional alias | `23` | `200/0` **8000** | `200/200` **0** |
| parameter source | `23` | `200/0` 8000 | unchanged — must not move |
| receiver source | `23` | `200/100` 4000 | unchanged |
| reassigned alias | `40` | `400/0` | unchanged — refused |
| alias chain | `23` | `200/0` | unchanged — refused |
| enum, rc payload | `10` | `150/0` 5600 | unchanged |
| struct array | `23` | `300/100` | unchanged |

Every exit code is `bin/fern -interp` and native x86-64 agreeing.

## `struct_type_of_slot` answers "what type name is on this slot"

#7368 recorded the reverted clause as retaining "enum names and dyn tags". That
was too kind to the field. Reproducing the crash rather than re-reading the note:
recompiling `conformance/cases/int_wrap` under the reverted clause gives exit
**139**, and the asm diff shows exactly one new `rc_inc`, in
`__fn___fern_i32_to_string`:

```
movq -8(%rbp), %rcx
subq %rcx, %rax        # 0 - n
movslq %eax, %rax
movq %rax, -24(%rbp)   # a PLAIN INTEGER
...
call __fn___fern_rc_inc
```

The retained value is the negated input. On `INT_MIN` that is `0x80000000`:
non-zero, even, and above `0x10000`, so it clears all three `__fern_rc_inc`
guards and dereferences `0x7FFFFFF8`.

So the field is set on slots holding **plain scalars**, not merely on enums and
dyn tags. Its writers give it at least five meanings: struct names, enum names,
`"clo"`, `dyn ` tags, and the ELEMENT type of a struct/enum array (where the slot
holds a buffer). No predicate built from it can gate a retain, narrowed or not.

## A clean theory that was wrong

The first hypothesis was const-folded struct literals: `mk()` compiles to
`leaq .K0(%rip)`, a struct in static storage, and `__fern_rc_inc` has **no
heap-base guard** where `__fern_str_free` does. It explained the segfault, and it
implied adding that guard.

Checking the emitted data first: `.K0` is preceded by `.quad -1` — the immortal
sentinel — in `.data`, not `.rodata`. `rc_inc` reads it, takes `js`, and returns.
Constants were already safe; the guard would have fixed nothing. **Worth keeping
because the theory was plausible, cheap to test, and false.**

## The predicate is the credit

`slot_is_reclaimable_struct`. It answers the operational question — was this slot
bound from a struct producer proven fresh and non-escaping — which is what makes
a slot hold a box:

- an integer slot cannot earn it (the crash above)
- an enum slot earns `"RCENUM:"` / `"SCENUMS:"` instead
- a struct-ARRAY slot earns `"ARRSTRUCT:"`
- a parameter is refused at its first line, a `dyn ` tag explicitly

Retain and credit become the same set by construction, exactly as the string
limb's `slot_is_reclaimable_str` does. Discrimination measured across three
compilers: `int_wrap` is `0` on main, `0` under the credit gate, `139` under the
type test.

## The alias takes a box-only release

The one place structs differ from the other three classes: a struct's release is
`__struct_drop_<T>` plus a box dec, and only the box was retained. The alias is
credited `"NODEEP:"` so its release stays box-only and the source keeps the single
field walk. Verified in asm rather than argued: `round` emits exactly one
`rc_inc` and exactly one `__struct_drop_P`. Two deep drops free `xs` twice —
exit 99, with `allocs == frees` at `live_bytes 0`, the census silent as always.

## 93% of the population is a refusal

| corpus | total | parameter | local | receiver | destructure |
| --- | --- | --- | --- | --- | --- |
| conformance/cases | **0** | 0 | 0 | 0 | 0 |
| examples/self_host | 173 | **161** | 9 | 2 | 1 |
| internal/stdlib | 12 | 11 | 0 | 0 | 0 |

Against string's 53%, so on this class the refusal is nearly the whole change.
The 12 non-parameter self-host sites are `var st: LowerState = s;` shapes whose
sources are receivers or reassigned loop locals, which earn no credit either — so
the creditable population in the compiler's own sources is close to zero, and
`internal/e2eselfhost` is the only coverage that reaches the credited case.

## Left open

- The **alias chain** stays refused, as in every other limb: the middle binding
  escapes as a bare ident, so it is not an eligible alias site and the source
  keeps no credit. Conservative — it leaks rather than over-releasing.
- **Receiver** sources are refused with parameters. Aliasing a borrowed value has
  nothing to share, so this is the class boundary rather than a gap.

## The emit-hash prediction was falsified, and my first attribution was wrong too

Pre-registered before the sweep finished: byte-identical across all 1494 rows,
because conformance has 0 struct alias binds and all 12 stdlib sites are
parameter-origin or a comment. Stated falsifier: any differing row means either a
creditable alias the origin count missed, or the retain firing where the credit
did not.

**Three rows differed** — `if_let_pattern_forms/main.fern`, all three backends.
The first branch of the falsifier was right: a creditable alias the count missed.

My first reading of *which* alias was wrong, and it is recorded because I
published it before bisecting. I attributed it to the as-pattern binder:

```fern
if let w @ P { x, y } = p { total = total + w.x + y; }
```

That shape is the one that bit #7368, it was visibly present in the fixture, and
it fit. Bisecting the fixture line by line says otherwise:

| probe | main | this change |
| --- | --- | --- |
| `if let P { x, y } = p` | `100/0` | **`100/100`** — rc_inc 0→1, arr_dec 0→4 |
| `if let w @ P { x, y } = p` | `100/0` | `100/0` — **unchanged**, rc_inc 0→0 |
| both together | `100/0` | `100/0` — unchanged |
| `if let P3 { x, .. } = q` | `100/0` | **`100/100`** |

The alias site is the **scrutinee of a struct-pattern `if let`**, which desugars
to a bare-ident `var` bind of the local. The `@` binder is not an alias site at
all, and combining it with the plain destructure *suppresses* that one's fix,
because `w` is a second binding of the whole value whose own use fails the gate.

So the fixture's 100 frees come from `q`, not from `p`.

## What the counting method could and could not see

The origin count was a regex over `var x: T = y;`. It cannot see any alias site
that is **produced by desugaring**, and that is a property of the method, not a
slip — nothing in `if let P { x, y } = p` looks like a binding of `p` at all.

The compiler's own alias scanner (`alias_bind_sites_of`) reads `StmtVar` nodes
with an `ExprIdent` init, i.e. it counts **post-desugar**. A pre-desugar text
search and a post-desugar AST walk are answering different questions, and only
the second one is the question that matters.

The correct method is behavioural: compile a probe for each candidate binding
form and diff the emitted `rc_inc`. That is how the table above was produced, and
it cannot be fooled by a desugar.

## Correctness of the changed emission

The fixture itself cannot check it. Its structs have constant fields, so they
const-fold to static storage with the immortal `-1` rc sentinel and every added
retain and release is a no-op (`allocs=3 frees=0` either side, exit 76 matching
both oracles). It detects *that* something changed and is structurally incapable
of saying whether the change was correct — the same signature as #7368.

Re-run with runtime-computed fields so the boxes are real: `100/0` → `100/100`
per shape, no underflow, exits matching both oracles. Pinned as
`struct_if_let_destructure_alias` and `struct_as_pattern_binder_leaks`, the
second existing specifically so the attribution cannot drift back.

## The `@` binder is an unfixed alias site, not a correct refusal

Calling it "refused" asserted a correctness claim that the rule above contradicts:
`w @ P { .. }` binds `w` to the whole scrutinee, which is the definition of an
alias site. The reason it earns nothing is mechanical, and it is a limitation
already on the books rather than a new one. `build_struct_match` caches the
scrutinee before binding:

```
var __sm.._v = p;     // alias level 1
var w = __sm.._v;     // alias level 2   (s_var_at, at_binding branch)
```

So `@` is the second link of an **alias chain**, and chains are conservatively
refused — the middle binding escapes as a bare ident, so the source keeps no
credit either. That is also the whole of the "suppression": adding `@` to a plain
destructure does not merely fail to add its own credit, it costs `p` the credit
the plain form would have earned.

Measured against hand-written analogues, which match exactly:

| shape | main | change |
| --- | --- | --- |
| one level — `var t = p;` | `100/0` | **`100/100`** |
| plain `if let P { x, y } = p` | `100/0` | **`100/100`** |
| two levels — `var t = p; var w = t;` | `100/0` | `100/0` |
| `if let w @ P { .. } = p` | `100/0` | `100/0` |
| `@` with `w` NEVER USED | `100/0` | `100/0` |

The last row rules out the obvious alternative explanation: it is the binding
that costs the credit, not any use of `w`. **Fixing the alias chain fixes the
as-pattern binder for free** — they are one limitation, not two.
