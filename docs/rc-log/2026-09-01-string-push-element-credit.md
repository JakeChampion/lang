# The string tier credits the push element (#7914 frontier, first payoff)

The full ownership walk of the driver's retained ~478 KB (#7914's
comment thread) ended at exactly one uncredited occurrence class:
`stringParamCounted` had no arm for `__method_Array_push`'s ELEMENT
position. The array tier gained that position in #7867 slices 1 and 4;
the string tier never did.

## What that cost, measured

A callee that stores its string parameter via `.append` — the checker's
per-block derived Scope (`Scope { names: s.names.append(nm), … }`) —
refused the parameter, so:

- every caller's FRESH argument temp (`child(w("a"))`) stranded — one
  64 B string per derived-scope construction, 14/round in the probe
  (44,800 B at 50 rounds), zero at exit on interp/native oracle terms;
- a caller's BOUND string passed there was escape-tainted (the
  `counted[pi]` exemption read false), stranding its buffer too;
- transitively, the checker's stranded Scopes pinned the module tables
  at rc 42 — the class-A chain.

Binding the child first, dropping the recursion, hoisting the string:
all measured identically until the string argument was hoisted, which
isolated the temp as the strand.

## The fix

One arm in `stringParamCounted`'s Call case: `xs.append(p)` marks the
element occurrence safe. Sound for the reason the array tier's arm is:
`emitArrayPush` emits the alias inc for a pointer element
unconditionally (`needsRcIncOnAlias`, and an Ident read is never a move
site), and the buffer's deep drop gives the reference back — so the
temp is at rc 2 after the call and the stage-(b) dec nets it to the one
owner either way. `everyOccurrenceSafe` still refuses a parameter with
any uncounted occurrence: pushed-then-returned-bare keeps its safe
leak, pinned in BOTH backends' leak gates as the deliberate refusal
watch.

## Measured

All four probe variants 0 (2300/2300; the hoisted-ident variant's
residual cleared through the same credit's taint exemption). Hazards:
double push balances, push-into-discarded-local balances,
bare-return refuses with zero underflows. **The self-host driver moved
for the first time in this arc: 478,992 → 417,200 B retained (−13%),
+217 frees**, on the 4-loop probe.

Two previously-pinned corpus leaks fixed and re-banked:
`string_array_append_grow_struct_field` 2800/2784 → 0 (both backends),
and the #7867 slice-2 launder pin — whose refused occurrence became
this credit's counted one, so the case now proves the two credits
COMPOSE and is renamed `copying_builtin_composes_with_push_credit`;
its watch role moved to the new
`string_pushed_then_returned_bare_stays_refused` pin (320 B x86-64,
448 B arm64).
