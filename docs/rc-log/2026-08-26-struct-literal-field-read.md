# A scalar field read off a struct literal releases the temporary

*2026-08-26* — the last of the three positions, self-host only; native clean.

## Where this finishes

`2026-08-26-struct-literal-argument.md` closed the call-argument position and
left one open. With this the three positions agree:

| unbound struct literal used as | before that slice | now |
|---|---|---|
| a discarded STATEMENT | clean | clean |
| a call ARGUMENT | 100/0, 200/0 | clean (#7576) |
| an intermediate FIELD READ | 100/0, 200/0 | **clean** |
| bound first: `var p: S = S { … }` | clean | clean |

Like the argument position it leaks per EVALUATION, and like it, the shape is
invisible to the construction-retain matrix, whose 35 cells all bind the literal
to `var p` first.

## The mechanism was here, for a different receiver

`lower_expr`'s `ExprFieldAccess` arm already reclaims the box behind a SCALAR
field read off a strict-fresh producer CALL (`mk().k`, #6491): stash the box,
read the field, deep-drop the rc fields while the box still owns them, then dec
it. A struct LITERAL receiver is the same temporary and takes the same release;
it simply was not admitted.

## The gate is a different question from the argument slice's

This is the part worth carrying forward. In argument position the question was
"can the callee keep this?", and the borrowability verdict the string and array
arms already use answered it.

Here there is no callee to ask. The hazard is that the read RESULT may alias a
field the release frees, so the gate is on the FIELD BEING READ: a scalar result
cannot alias anything, which makes the deep drop unconditionally safe with no
verdict standing behind it. That gate happens to cover both measured leaks,
since both read scalars — so it is a complete fix for what was broken, not a
partial one.

An rc-field read (`(A { … }).xs`) stays refused. Admitting it needs a retain this
read cannot prove.

## The refusal is load-bearing, and the failure is loud

Dropping the scalar gate puts the churned rc-field probe at **802 frees against
800 allocs** — more frees than allocations, a double free — with the leakcheck
summary line itself corrupted.

It also makes the plain `rc_field_read` case read a clean 200/200 where the
correct compiler reads 200/0. That is the FIFTH slice in a row where a
census-only comparison scores the broken build higher than the correct one. The
test therefore asserts `frees <= allocs` on every case, admitted or refused —
frees exceeding allocs is the one census signal this class cannot fake.

String / enum / map / tuple / option fields keep `struct_fields_reusable` false,
so a struct carrying one still leaks whole; that safe-leak floor is pinned
unchanged by `string_field_struct`.

## Verification

`internal/e2eselfhost/self_host_struct_lit_fieldread_test.go`, 6 cases: the two
fixed shapes, two refusals (one churned), a churned read-back of the admitted
shape, and the string-field floor. Every want confirmed against BOTH oracles.

`TestSelfHostStage2FixpointArm64` green (163 s); the targeted rc set green
(372 s), including both construction matrices against their pinned files.
