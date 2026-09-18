# Where the lift's duplicate loop came from

#9688 is fixed on main in `05d1b89a`: the loop-end handler left `cur_id`,
`cur_insts` and `cur_term` sitting on a block it had just appended, and the
function's own flush appended it again. This note records the half that commit
does not answer — which loop, given that none of the 55 functions it names
contains one.

## None of them has a source loop

`ssarc.walkable` is six `if let`s and a `return`. `semrecords.resolved` is
three `if`s and a `return`. `semtypes.equal`, `enumcontract.same`, the
`astwalk.fold_*` family, `lexer.tokenize_impl` — instrumenting the lift to name
every function reaching the loop-end path on the physical-RC bundle gives 59
hits, and what they have in common is not a loop. It is that every one of them
is **self-recursive**.

`irlower.tco_self_tail` wraps a whole function body in `loop { … } end` so a
self tail call can jump to the header instead of growing the stack. The wrapper
is the loop, the function's body is the loop's body, and a function body ends in
a return — which is exactly the condition, a body that reaches the loop's `end`
alive and already terminated. `walkable`'s asm shows it: a backward
`b .Lssa_ssarc__walkable_1` and no `bl __fn_ssarc__walkable` anywhere, the
recursion gone into a loop nobody wrote.

## Why no one could shrink it

The trigger is narrower than "a TCO'd recursive function" — several small
recursive programs written to match the shape were not wrapped at all, and no
hand-written loop reproduces it: `while` and `for`, bodies ending in `return`,
`break`, `continue`, nested, none reach a loop `end` alive with a terminator,
because the back-edge `br` the lowering appends terminates the body first. The
repro that works is the checked-in `physicalRCBundle(physicalRCCases())` built
for `arm64-linux`, and the regression test drives the op stream directly for
the same reason.

## The bit that generalises

A construct that exists only after lowering has no source shape to search for,
so "no program does this" is not a conclusion that can be reached from the
corpus. `tco_self_tail` is one of several passes that synthesise scopes; a lift
invariant that holds for every written loop can still fail on a synthesised
one. Reach for the instrumented lift over the source grep — naming the function
took one `eprint` and one rebuild, after three wrong guesses from reading code.
