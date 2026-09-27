# An enum-field alias holds a count

On the AST lowering, `var g = h.e` (a direct enum field of a struct) bound an
uncounted alias of the struct's enum box. Rebinding `h` released the box, the
next same-size allocation reused the block, and `g` read the new occupant
(#10310): 93 instead of 3 with a balanced census and no underflow. Passing the
alias to a call, declaring it in an `if` block, or binding it in a function
with no loop all failed the same way.

The bind now dups the box, as native does, and the local takes the release the
struct gives the same field. That release is rc-gated because the alias is
never the sole owner: where the struct's own field release deep-drops the
enum (`enum_arr_elems_walk_ok`, scalar-array payloads) the alias joins
`RCENUM:` / `RCENUMS:`, whose payload walk runs under `__fern_rc_is_unique`;
for every other enum it joins `SCENUMS:`, a box-only `__fern_rc_dec`, because
another payload may have reached the box uncounted. Whichever owner releases
last does the payload work, in either order.

- `collect_enum_field_alias_sites` tags `EALIAS:` an alias that is never
  reassigned and does not escape by either the plan, which forgives a counted
  sink such as an append, or the enum-field walk. The walk is needed because
  the plan taints an alias of a source it cannot type: gating on the plan alone
  left a method reading `self.e` with a dup and no release, 500/200 where the
  base was 500/500. `credit_enum_field_alias` turns the tag into the credit in
  the `VarTypes` field-access arm, where `alias_inc` is set, so the dup and the
  credit are one decision.
- The plan's `rc_fe_rhs_tainted` ports the direct-enum case of native's
  struct-source field arm, so the alias is free-eligible. `TestSelfHostRcPlanDiff`
  `enum-field-extract-bind` now agrees with native on `freeEligible` and
  `lastUses`.
- The loop re-declaration of a credited alias carries its `alias_inc`: the dup
  lands before the gated release, so an unchanged box only loses the alias's
  previous count.

Three releases the counted alias reached were wrong on their own:

- `emit_struct_enum_deep_reinit_store` deep-dropped a struct's enum field
  without asking whether the box was shared. With a counted alias the payload
  was freed twice (underflow). It now uses the gated drop.
- The `SCENUMS:` exit sweep did not skip a slot a construction had moved
  (`moved_elided`), which the `RCENUMS:` sweep already did. A fresh scalar enum
  moved into a struct at function scope over-released on base as well.
- The move-on-return keep set had no enum clause, so a returned `SCENUMS:` or
  `RCENUMS:` local was released by its own frame's sweep. A returned fresh
  scalar enum local answered wrong on base.

Still open: an enum call result is released by no caller credit unless its
callee returns a direct constructor ("RCE:" and the scalar fresh-ret row), so a
returned alias or local now leaks in the caller where it used to be freed
before the caller read it (#10365). An alias the collector does not admit
takes no dup, so the dup and the release stay co-extensive, and it is still
the uncounted borrow #10310 describes: a reassigned alias (`var g = h.e; … g = A(5);`
answers 11 where 18 is right) and one assigned to an outer local (`last = g`
answers 93 where 3 is right) still read a recycled box after the struct is rebound. Dup'ing those
too leaked on shapes base and native keep clean
(`TestSelfHostOwnParamLiftedFieldLeakCheck/enum-field-lift-control`, an alias
reassigned from an `own` call), so they need an assign-path alias release
first. A string-payload alias frees its box and leaks the string, as the
struct side does.

Measured on x86-64, AST lowering (`FERN_SEM_IR=`), 100 rounds, exit then
allocs/frees; the semantic lowering and `-interp` give the "after" answer on
every row:

| shape | before | after |
|---|---|---|
| #10310's program, `A(i32)` payload | 93, 500/500 | 3, 500/500 |
| same with an `i32[]` payload | 0, 700/700 | 3, 700/700 |
| same with a `string` payload | 15, 1100/1000 | 96, 1100/1000 |
| alias passed to a call after the rebind | 93, 500/500 | 3, 500/500 |
| alias bound in a function with no loop | 93, 500/500 | 3, 500/500 |
| alias shared into a second struct, fn scope | 99, 6/6 | 7, 6/6 |
| fresh enum moved into a struct, fn scope | 99, 300/300 | 3, 300/300 |
| returned alias | 93, 300/300 | 3, 300/200 |
| returned fresh enum local | 93, 200/200 | 3, 200/100 |
| alias appended to an array | 3, 507/407 | 3, 507/407 |

`TestSelfHostEnumFieldAliasCount*` pins the balanced shapes on x86-64 (leak
census and sanitizer), arm64 and wasm under the semantic, AST and two mixed
lowerings; `TestSelfHostEnumFieldAliasReturn*` pins the returned shapes by
answer and underflow.
