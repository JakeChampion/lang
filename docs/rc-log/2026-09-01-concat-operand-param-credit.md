# The string tier credits the concat operand (#7914 frontier, second payoff)

Re-ranking the self-host driver's retained bytes after the push-element
credit put two new rows at #3 and #4 by weight: `irlower__borrow_reg_new`
(10 blocks / 30,720 B) and `irlower__borrow_reg_put` (8 / 24,576 B) — the
interprocedural borrow/consume-safe fixpoint's bucket registries, 3,072 B
each.

## What the walk found

Every one of the ten registries a compile builds was stranded, seed
generations included, and each read **rc 0 with zero holders** at exit —
not a missing decrement but a decrement that never freed. That is the
signature of `__fern_rc_dec`, which only decrements; the freeing release
for a `string[]` is `__fern_drop_arr_str`. Disassembling
`consume_safe_params_interproc` confirmed it: every array rebind and the
whole exit sweep emitted the dec-only form.

Dumping `computeFreeEligible` for that function named the cause in one
line — `reg`, `prev` and `next` were all tainted, and so was the string
local `flags`. `flags` is passed to

```
function borrow_reg_put(reg: string[], key: string, flags: string): string[] {
    var b: i32 = util.hash_bucket(key, reg.len());
    return reg.with(b, reg[b] + key + "|" + flags + "\n");
}
```

whose only use of it is as a CONCAT OPERAND — and `stringParamCounted`
had no arm for that. Uncredited, computeFreeEligible's native
single-word-string rule taints the caller's `flags`; `rhsTainted`'s
counted-argument check then reads a tainted argument at an uncredited
position and taints `next`; `reg = next` and `prev = reg` carry it to
both other array locals. One missing string arm cost three 3 KB buffers
per fixpoint pass.

## The fix

A `*ast.Binary` arm in `stringParamCounted` marking both operands of a
string concat or comparison. `__fern_strcat` allocates a fresh buffer, or
returns an SSO-inline value or the shared empty sentinel, and never hands
back either operand's pointer — the same fact `stashOwnedStringOperand`
states ("a BORROWING string op — one that reads its operand's bytes and
leaves that buffer alone") and `rhsTainted`'s `IsStringConcat` case
already relies on. A comparison yields a bool, which can alias neither
side. The in-place `s = s + rhs` append is not an exception: it needs
`freeEligible[s]`, which a borrowed param never has.

Concat is the commonest non-retaining use a string parameter has, and it
was the last occurrence kind with no arm.

## Measured

x86-64, `FERN_LEAKCHECK=1`, `__rc_underflow_count()` folded into every
exit, `bin/fern -interp` as the oracle:

| probe | before | after |
| --- | --- | --- |
| registry fixpoint, 10 rounds | 371,360 | 2,240 |
| registry fixpoint, 20 rounds | 742,720 | 4,480 |
| corpus `.append` form | 2,752 (93 allocs / 13 frees) | **0** (45 / 45) |
| corpus `.with` form | 4,032 | 384 |

**The self-host driver: 416,944 → 367,872 B retained (−49,072, −11.8%),
+185 frees**, and the compiled output is byte-identical. Across the two
credits the arc now stands at 478,992 → 367,872 (−23%).

arm64 is byte-identical on every probe: the credit lifts a taint that
only the native single-word string ABI applies.

## The residue, root-caused

The `.with` form's remaining 384 B is a DIFFERENT defect, found on the
way and pinned in both leak gates as
`concat_operand_param_rewrites_registry_buckets`:
`__fern_arr_cow_inplace_ptr` incs each element into the copy it hands
back (it must — otherwise both buffers share elements under one count),
but the caller's dec-on-overwrite for an array local is the buffer-only
`__fern_arr_dec`, whose justifying comment is about the push MOVE-grow
helper, where elements transfer without an inc. So the old buffer dies
owing one reference per element the copy retained. The plain
`a = f(...)` overwrite has it too — `var a = mk(); a = mk();` on a
`string[]` strands one element per round — which makes the fix the
overwrite's, not the cow's.
