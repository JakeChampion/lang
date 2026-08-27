# A map declared inside a block was never freed — the "MAP:" credit resolved by name

`slot_is_reclaimable_map` asked `reclaim_has(reclaimable_names, "MAP:", s.locals[i].slot_name)`.
`retire_locals` renames a block-scoped local to `!retired!<name>` when its block
exits, so the lookup missed for every map not declared at function scope, and the
exit sweep swept nothing. Measured over the 2000-round steady window, x86-64:

| shape | base | site-keyed |
|---|---|---|
| `Map[i32, i32]` in an `if` body | **62 pages** | 0 |
| same map declared in a `while` body (3 rebinds/call) | **62 pages** | 0 |
| `Map[string, string]`, fresh keys + values, in an `if` body | **62 pages** | 0 |
| the same map at FUNCTION scope — the control | 0 | 0 |

62 on arm64 too, 82 on wasm; the interpreter and native are flat on all four.
The control is the whole isolation: one indentation level, nothing else changed.

## The fix is the key, not a scoped variant

The four collector entries (`"MAP:"` and its `"MAPVS:"` / `"MAPKS:"` / `"MAPVA:"`
column tags) now carry the binding-SITE key `name@line:col`, and all four
consumers read it through `slot_credit_at`, which resolves off `LocalInfo.reclaim_site` —
carried on the SLOT, so the rename cannot hide it.

The struct class needed a `_scoped` sibling for this (#6127 / #7349): its
predicate has fourteen consumers, and the ones that are not the sweep are not
sole-owner for a block-scoped slot, so flipping it wholesale segfaulted the gen1
self-compile. The map class has **three**, and the two that are not the sweep are
both the loop-reinit free — free the prior iteration's box before storing the
next — which is exactly the block-scoped case. A split verdict between them
would be the harmful shape here: the sweep freeing a box a reinit already freed.
So they take the key together, and no strict/scoped pair is needed.

## Why the column tags had to move in the same change

`"MAPVS:"` / `"MAPKS:"` / `"MAPVA:"` are derived from the same collector entry as
the base credit. Converting only `"MAP:"` would give a block-scoped map its base
credit while its columns resolved nothing — a shallow `__fern_map_free` where a
`__fern_map_free_kvs` was owed, so the key and value boxes leak one level down
while the byte count improves. That reads as partial progress, not as a bug; it
is the same "two keys off one string, silently" shape #7358 records for
`"RCENUM:"` / `"RCENUMS:"`.

## The over-release direction, and why it was not witnessed

Two `m` in sibling `if` arms — one a fresh literal, one a bare alias of a
parameter — is the shape that hands an aliased binding a credit it does not own.
It measures **0 on both sides**, because the retirement that causes the leak also
hides the collision: both slots are renamed, so neither resolves the credit. The
leak was masking the fault. The case is pinned anyway
(`sibling-alias-no-over-release`), and it is the one that goes red if the key is
ever taken back to the name — now that the scoped slots resolve, a name key would
free the caller's map under a live reference rather than merely leaking.

## The instrument note

`FERN_LEAKCHECK`-style alloc/free arithmetic is not what caught this and would not
have: the steady-window page delta is. The three leaking shapes and the control
have the same call profile in the emitted asm apart from the `__fern_map_free*`
call the sweep does or does not emit — `grep -c 'call __fn___fern_map_free'` over
the block-scoped program returns 1 (the definition alone) at base and 3 with the
site key, which is the cheapest positive check that the release landed where it
was owed rather than somewhere else.

## Next lead: the two remaining `slot_name` lookups are NOT this bug

`"ENVCAP:"` and `"SNAP:"` are the last reclaim-adjacent families still resolving
through `s.locals[i].slot_name`, so they read like the obvious next slice. Checked
rather than assumed, and neither can reach the retirement hazard:

- `"SNAP:"` — `slot_is_snapshot_param` returns false above `i >= s.n_params`. It
  answers for PARAMS only, and a param slot is never retired.
- `"ENVCAP:"` — its producer walks `fn.body` at TOP LEVEL only, over the
  `var cap = __env[1+i]` reads `make_clo_func` synthesizes at the head of a
  hoisted `$clo` body. Those statements are compiler-generated at function scope;
  there is no nesting for `retire_locals` to reach.

What is left on both is the *collision* half rather than the scope half — a user
local sharing a capture's name later in the same closure body would inherit the
`"ENVCAP:"` exclusion and lose its release. That is a leak-direction hazard on a
shape that needs an escaping closure to construct, so it is worth a probe but it is
not the same class as this fix, and anyone reaching for it expecting another
62-page block-scoped leak will not find one.

## Instrument reach: this change has no creditable site in the tree

Worth counting rather than assuming, because the number decides which gate carries
signal. Fresh map-local declarations, by whether they sit at function scope or
inside a block:

| corpus | function scope | inside a block |
|---|---|---|
| `conformance/cases` | 43 | **0** |
| `internal/stdlib` | 8 | **0** |
| `examples/self_host` | 11 | **0** |

Nothing in the tree declares a map inside an `if`, a loop, or a match arm. So the
emitted asm is byte-identical between compilers built either side of the change
across all 503 conformance cases — verified, no diffs and no exit-code changes —
and the self-compile fixpoint cannot move either. Both are **scope** instruments
here: they say nothing else changed, and they carry no correctness signal
whatsoever about the class being fixed.

The only instrument with reach is the new suite, which is why it is worth its five
cases across three backends: each of its three leaking shapes is red at base with
its own page count, on x86-64, arm64 and wasm. This is the same reach arithmetic
#7282's string limb recorded — "conformance/cases has 0 instances" understates a
gate and "fixtures link stdlib" overstates it; the number that matters is how many
reachable sites survive the class's own admission gates, and here it is zero on
both counts.
