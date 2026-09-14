# An aliased `a = a.append(v)` releases the buffer it grew away from

#9191, from the `.with` probe set: `var b = a; a = a.append(x)` read 2 / 1
with 48 live bytes, the answer right and the census not.

## Cause

The append self-reassign picks its push by a static alias credit
(`is_aliased_name`): a sole owner takes `arr_push_owned`, which frees the
superseded buffer on a grow, and an aliased target takes the plain
`arr_push`, whose contract is to leave the old buffer to whoever holds it —
in place it hands back the same pointer, on a grow or an un-share copy a
fresh rc 1 buffer with the old one untouched. The store after it was a bare
`store_local`. So the reference this frame held of the original was never
released: `b`'s sweep took it from 2 to 1 and nobody took it further.

The same store ran after EVERY later grow of the same target, since the
credit is per function: a hundred appends onto an aliased local leaked the
un-share copy and each doubling after it, 7 / 1 with 1,656 live bytes,
where the sole-owner form reads 7 / 7.

`for x in a { a = a.append(…) }` is the same pairing one step removed. The
foreach binds a hidden snapshot `var $forit = a` (`lower_foreach_snapshot`),
which the ordinary ladder retains, but the credit is computed from the
source (`aliased_array_names_of`) and never saw the hidden bind, so the
target took `arr_push_owned` at rc 2: the owned grow copies, sees the old
buffer is not sole-owned, and leaves it — 2 / 1 again.

## Change

- The store after an aliased local's plain push releases the slot's old
  reference when the pointer changed (`emit_arr_store`'s cow guard;
  `aliased_push_releases_old`), on all three element widths. In place the
  pointer is the same and nothing is released; on an un-share the original
  goes from 2 to 1 and the alias keeps it; on a grow of a buffer only this
  frame holds it is freed. Gated on `arr_slot_shallow_release_ok`, the exit
  sweep's own condition for a bare box dec, so a parameter (its store is the
  ownership flag's) and the pointer-element classes are untouched.
- `lower_foreach_snapshot` credits the iterable as aliased
  (`note_aliased_name`) before binding the snapshot: the hidden `var` is the
  alias the source scan describes, bound by the lowering instead.

After the un-share the target is unique again and the plain push writes in
place, so appends after the loop cost nothing extra: a foreach snapshot
followed by a hundred appends reads 7 / 7, and 6 / 6 on native.

## Measured

Self-host x86-64, `FERN_LEAKCHECK=1` at emit, interpreter and native as
oracles (native balances every row):

| shape | before | after |
| --- | --- | --- |
| `var b = a; a = a.append(40)` | 2 / 1, live 48 | 2 / 2 |
| the same, 100 appends | 7 / 1, live 1,656 | 7 / 7 |
| both holders grow | 3 / 2, live 48 | 3 / 3 |
| `i64[]` / `f64[]` | 2 / 1 each | 2 / 2 each |
| `for x in a { a = a.append(…) }` | 2 / 1, live 48 | 2 / 2 |
| the foreach, then 100 appends | 7 / 6, live 48 | 7 / 7 |
| `xs = xs.append(7)` on a borrowed param with a local alias | 2 / 2 | 2 / 2 |

The rest of the alias-kind probe set is unchanged row for row. `string[]`
(2 / 1) and `i32[][]` (5 / 2) under the same shape are the pointer-element
classes the gate leaves alone, and stay leak-only.

The compiler-sized residue is not this shape either: `od -t fL` over 300
bytes and `printf '%e %g %f\n' 4e-4951 ×3` read the same census before and
after (27,634 / 26,510 and 11,072 / 10,909), byte-identical output.

## Gates

Five rows in `TestSelfHostWithCowIR{X86_64,Arm64,Wasm}`: `alias-append`,
`alias-append-loop`, `alias-append-both`, `alias-append-wide`,
`alias-append-foreach`; each fails its census on the parent commit. Also
green: the append, alias and foreach families of `internal/e2eselfhost`,
the whole-compiler emit-all fixpoint, the complexity ratchet (the arm's
`||` moved into a local so the count is unchanged), `make fmt-check`.
