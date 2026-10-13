# 2026-10-13 — a member binding reads its scrutinee

`semsource.field_read`, `ssaunits.likely_unique` / `member_of` / `rebuilds` /
`rebuilt_read` / `variant_slots` / `known_variant`, `ssarc.known_fields`,
`checker.ow_arm_payload_ctx` / `ow_arm_lent_ctx`, and the Go checker's
`FieldMoveLiteral` and arm bindings. Refs #8171. Emitted bytes change at a
match over a struct-union whose scrutinee the frame owns.

## The shape

A struct-union's member is its own box, so `match (t) { Node(n) => … }` binds
`n` as the scrutinee narrowed to `Node` (`variant_as`), and `n.left` was a
`record_get` of that second value. The narrowed value holds no unit of its
own, and the ownership walk reads it as a projection it cannot take through:
a field read of `n` neither took the field out of an owned `t` nor named the
variant `t`'s box holds, so `t` dying in the arm never served the arm's
construction as a donor. An owned tree rebuilt arm by arm allocated a fresh
node per node and released the old one. The AST rewrite passes rebuild about
31.6 M `ast.Expr` nodes over one compile of the compiler, each a box in and a
box out; this is the compiler half of taking that away, the passes' half
(owned trees through `astwalk`) is open, and blocked on the language: an
element read cannot feed an owned parameter (E051), so a statement list's
children cannot be moved out in a loop.

## What changed

A field read of a member binding is lowered as a payload read of the
scrutinee, `variant_get(t, k, "Node")`, the shape a declared enum's arm has:
the planner's payload take, its known-variant drop and its reuse pairing
apply. The pairing learns that a record construction of a member rebuilds a
struct-union donor of that member, so the fields it reads back stay in place
and its other reference fields are moved out, under the one uniqueness read
the keep makes. The narrowed value remains for the places that use the
member whole.

The two checkers agree on what the binding is: on an owned scrutinee a member
binding is owned, bare or through the superseded-field move
(`Node { ...n, left: bump(n.left) }`), and a member literal returned as its
union is still that move's literal; on a borrowed scrutinee the binding is
lent, as a payload binding is. The self-host admitted the move on a borrowed
scrutinee and refused the bare binding on an owned one; the Go checker
refused both because the union return type had wrapped the literal in the
member's variant call. Pinned by `TestSelfHostCheckerCodesX86_64`'s
`e051-member-*` rows and the Go `TestOwnGuardMemberBinding*`.

## The trap

Applied to every owned struct-union value, the speculation cost 2.5% of the
compile: the compiler's tokens, nodes and types live in arrays and records,
so a member read out of one (`let t = p.peek(); match (t) …`) is shared with
its holder, and every take tested the box and retained, every pairing tested
the donor and allocated through the token, and every known-variant death
walked its fields inline — more code for every pass to carry and a test lost
every time. `likely_unique` now gates the three for struct-unions: a box is
plausibly the frame's alone when it is a parameter declared `own`, a
construction, or a phi or copy of those, or when a payload take made it the
frame's.

Two weaker predicates were measured first and are the traps. "Any owned
unit" admitted a call's result, and `let t = p.peek(); match (t) …` reads a
token the array still holds. "Any owned parameter" admitted the parameters
the ownership inference makes owned, and `type_key(t)` is called with
`rec.ty`, a retained field, at its last use: the inference moves it, the box
arrives shared, and the arm's takes and inline drops doubled the function.
A parameter declared `own` is different in kind: E051 makes its callers hand
over a fresh value, another owned one or a local at its last use. The
declaration is spelled on the parameter's instruction (`str` of kind 6) so
the planner can tell the two apart under the inferred modes.

## Measured

The stage-2 compile of `checker.fern` to a binary under callgrind, each
compiler built from its own tree by itself, both compiling one frozen copy of
main at 6fdb1671, warm certifier memo. The base is the branch before this
slice, which then also carried four runtime fast paths (the release helpers,
the array box and the first append) that were later pulled from the PR to
wait for their text-machine proofs; the deltas are this slice's alone.

| | Ir |
|---|--:|
| base | 14.532 G |
| member reads, ungated | 14.897 G (+2.51%) |
| gated on any owned unit | 14.877 G (+2.37%) |
| gated on a declared `own` parameter | 14.523 G (−0.07%) |

The slice is neutral for the compiler's own compile: what it buys is the
owned rebuild below, which the passes do not yet use.

A 63-node tree of two member kinds rebuilt four times in place allocates
nothing past its construction (`TestSelfHostMemberMatchReuse`); before, each
round allocated 63 boxes. The compiler rebuilds itself byte for byte.
