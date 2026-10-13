# 2026-10-13 — a record or an enum is a box

`ssarc.proven_box`; the native emitters' `ssa_rc_guard` and
`ssa_unique_flags` read `i64_imm` 5. Refs #11944.

## The shape

An inline retain or uniqueness test guards the count it reads twice: a tag
test for an inline string, and a compare with the heap floor for null or a
scalar. On x86-64 that is four instructions before the count is touched:

```
testb $1, %r14b
jnz   done
cmpq  $0x10000, %r14
jb    done
cmpl  $0, -8(%r14)
js    done
addl  $1, -8(%r14)
```

A value whose type is a record or an enum is never an inline string, and it is
null only where the lowering zeroed it. Every payloadless variant and every
record with nothing counted is an immortal box rather than a null, so the
count's sign is the one test such a value needs. rbtree's insert retains and
tests nothing else.

## The rule

`aligned_rc` already marked a retain or uniqueness test of a typed value with
no byte-view tag (`i64_imm` 4). A value whose type is in the function's record
or enum table is now marked 5, and both native emitters leave out the tag and
floor tests for it. The marks sit only on loads of the function's own values,
never on a drop helper's scratch slots, where a taken field reads back null.
Wasm calls the runtime's guarded helper and is unchanged.

A value the lowering may hold null keeps its guards: a zero (`ssasem.zero`),
or a phi, copy, variant retag or cast a zero reaches (`nullable`). A
value-position match's local starts at zero, so json_parse_result inlined into
a loop retains its result on a path where it is still null; the first version
of this change read that count unguarded and segfaulted the utf8_ingest
benches (`proven_box_zero_test.go`).

## Measured

x86-64 retired instructions, on top of the token batching, outputs and
censuses identical:

| entry | before | this change |
|---|--:|--:|
| perceus_rbtree | 57,549,089 | 54,928,289 (−4.6%) |
| perceus_rbtree_ck | 67,557,807 | 64,570,287 (−4.4%) |
| perceus_cfold | 63,825,267 | 61,318,067 (−3.9%) |
| perceus_deriv | 89,717,608 | 87,356,008 (−2.6%) |
| perceus_nqueens | 104,479,136 | 102,759,936 (−1.6%) |

A stage-2 compiler built with the change compiles the compiler, and stage 3
is byte-identical to it.

## Not covered

A release keeps its floor test: `__fern_rc_dec`'s mark is the certifier's
(a shell's release is 2), so the proof would need a field of its own. A
uniqueness test's unique arm then releases a box the token already proved
holds a count of one, and still reads the count twice before it frees.
