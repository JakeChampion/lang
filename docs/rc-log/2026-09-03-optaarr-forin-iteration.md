# 2026-09-03 — `for o in xs` over an `Option[T[]][]` costs the whole OPTAARR credit (#7414)

`arrarr_row_escapes` rejected every `for o in xs` outright for the box-element
classes, so an array of Options iterated instead of indexed kept nothing but the
shallow buffer dec: every option box and every payload buffer leaked.

```fern
function round(i: i32): i32 {
    var keep: Option[i32[]][] = [Some([i, i + 1]), Some([i + 2, i + 3])];
    var t: i32 = 0;
    for e in keep { match (e) { Some(p) => { t = t + p[0]; }, None => {} } }
    return t + keep.len();
}
```

## The issue's diagnosis was for the wrong class

#7414 attributes this to the `ARRENUM:` credit and its deliberately
`.len()`-only escape gate. It is not that class. `Option[i32[]][]` is
`optaarr_ann_is`, and the emitted release is `__fern_optarrarr_free` — visible in
the asm for the `.len()`-only control, which already balanced. Three probes
localise it with nothing else changing:

| body of the loop over `keep` | self-host, 150 rounds |
|---|---|
| `keep.len()` only | 755 / 755, live 0 |
| `match (keep[0]) { Some(p) => … }` | 755 / 755, live 0 |
| `for e in keep { t = t + 1; }` — element never touched | 755 / 151, live 24160 |

The element is not read at all in the third row. Only the ITERATION was ever the
difference, and `arrenum_esc_expr` — whose comment the issue quotes — is not on
this path.

## Measured

`FERN_LEAKCHECK=1`, x86-64, 150 rounds (50 + 100), native `bin/fern` against
`bin/fern-selfhost`:

| probe | native | self-host before | self-host after |
|---|---|---|---|
| the repro above | 755 / 755, live 0 | 755 / 151, live 24000 | 755 / 755, live 0 |
| `None` mixed in, `p.len()` borrow | 755 / 755, live 0 | 906 / 151, live 30200 | 906 / 906, live 0 |
| `Option[f64[]][]` payload | 755 / 755, live 0 | 755 / 151, live 24160 | 755 / 755, live 0 |
| `.len()`-only control | 755 / 755, live 0 | 755 / 755, live 0 | unchanged |
| `match (keep[i])` control | 755 / 755, live 0 | 755 / 755, live 0 | unchanged |

160 B/round, flat. Every row answers identically on both compilers before and
after, and `__rc_underflow_count()` is 0 in all of them — which is why no exit
code and no differential could see this.

## The admission

The loop var holds an element BOX borrowed with no retain — measured: the
self-host emits zero `rc_inc` for it, exactly as `arrarr_row_escapes_iter`
records for an arr-of-arr row — and the bind is transient, the loop ending
before the exit sweep. So the index bind and the iteration are separate
questions, and `arrarr_row_escapes_ex` now takes them separately: `dup_at_index`
still governs `var o = xs[i]` (refused here, no dup), and a new `box_iter_ok`
governs the `for`.

What the iteration needs is a confinement proof for a box the ordinary escape
walk cannot read, because it is consumed by a `match` that walk treats as an
escape. Two conjuncts:

- `body_unsafe_for_match_borrow` on the loop var — the box is admitted in
  exactly one position, the bare scrutinee of `match (o)`, and refused as an
  alias, a call argument, a store or a return;
- `elem_box_iter_bind_escapes` — every binding such a match hands out (payload,
  extra payloads, and the at-binding, which is the box itself) stays inside its
  arm, vetted by `elem_arm_bindings_confined` and so by the STRICT
  `binding_escapes_arm`.

`binding_escapes_arm_scrut` is deliberately not used. Its own contract limits it
to a scalar inner payload, which is exactly what this class's rc array is not —
the obligation #7414 says is missing, and this proof does not borrow it.

`arrenum_param_arm_ok` is renamed `elem_arm_bindings_confined`: it was already
class-neutral and is now called from two classes.

A guarded arm refuses. `binding_escapes_arm` does check the guard, so this is a
narrowing rather than a demonstrated hazard — the same one
`consumed_rcpayload_enum_frees` makes next door, because under a guard which arm
ran is not syntactic and this proof is per-arm.

## The hazard is witnessed, not argued

With both conjuncts stubbed to `false &&` and nothing else changed:

```fern
function pick(i: i32): Option[i32[]] {
    var keep: Option[i32[]][] = [Some([i, i + 1]), Some([i + 2, i + 3])];
    var last: Option[i32[]] = None;
    for e in keep { last = e; }
    return last;
}
```

with three `[7777, 7777]` allocations in the caller to recycle the freed block,
the self-host exits **100** (the payload reads back as 7777) where native exits
0, at allocs 1350 / frees 1200. The box alias takes no counted claim, so the
sweep frees the payload the returned Option still names.

Two things about that run are the reason this class needs the leak matrix rather
than an exit differential:

- `__rc_underflow_count()` is **0** throughout. The use-after-free is a read,
  not an over-release.
- `FERN_SANITIZE=1` exits **0** — its quarantine keeps the freed block out of
  the recycling path, so the stale read returns the right answer. The sanitizer
  MASKS this shape; do not read a green sanitize leg here as evidence.

Without the stub the same probe exits 0 and takes the leak-safe fallback.

## Refusals pinned at their leak

`TestSelfHostOptaarrForInIter{X86_64,Arm64,Wasm}` asserts `live_bytes > 0` on
every refused row, not just its exit code: guarded arm, payload binding stored
out, payload handed to a call, loop var stored out, bare `var o = xs[i]`, and
the two out-of-class annotations (`Option[string[]][]`, whose payload is not a
leak-safe scalar array, and `Option[Option[i32[]]][]`, which is not an option of
an array at all). A balance appearing there means the credit reached a shape
this proof does not cover.

The cost of the strict vet is one row that would have been safe: an arm payload
stored to an outer local takes a counted retain (`store_out_takes_counted_claim`),
so `out = p` inside the arm is sound and is refused anyway. Admitting it needs
the payload-type reading that predicate carries, threaded into the arm vet.

## Trap for the next reader

`var o: Option[i32[]] = keep[0];` does not compile under the self-host: its
checker types the element as bare `Option` with the payload dropped, and rejects
the annotated bind (`E003: cannot assign Option to variable of type i32`) where
native reports `Option[i32[]]` and accepts the annotated form. An unannotated
`var o = keep[0];` is the only spelling of an OPTAARR element bind the self-host
accepts, which is what the `elem-bind-refused` row uses. That is a self-host
checker gap in generic-payload inference, not an rc one, and it is untouched
here.

## Gates

`TestSelfHostOptaarrForInIter{X86_64,Arm64,Wasm}` (13 rows each, no skips); the
four newly-admitted rows verified FAILING on the parent at the exact figures
above. x86-64 per-module emit-all fixpoint (batch=8, 55 units, gen0 == gen1);
`TestSelfHostStage2FixpointArm64`; the self-host fixtures legs on all three
targets; the OPTAARR / OPTARRARR / ARRTUP / ARRARR / ARRENUM / STRUCTARR /
alias-bind / leak-matrix / rc-plan-diff suites in `internal/e2eselfhost`;
`internal/lint`; `TestSelfHostFeatureCensus` (wildcard arms unchanged at 2953 —
the walker's one new `_` arm pays for the one `ident_named_is` removes).
