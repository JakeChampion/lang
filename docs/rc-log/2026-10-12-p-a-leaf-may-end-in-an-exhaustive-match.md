# 2026-10-12 — a leaf may end in an exhaustive match

`seminline.leaf_body`, `seminline.code_blocks`.

## The shape

```fern
function is_red(t: Tree): boolean {
  match (t) {
    Node(Red(), _, _, _, _) => { return true; },
    _ => { return false; }
  }
}
```

rbtree's `ins` calls `is_red` twice, so it is spliced only as a leaf, and
two things kept it from being one:

- The match ends in the unreachable block an exhaustive match leaves, and
  `leaf_body` admitted only return and branch terminators. `spliceable`
  already admits it for a function called once (#12217).
- The match lowering leaves eighteen blocks, most of them a bare jump, and
  the leaf limit of sixteen counted them all. Eight of them emit anything.

Each call cost a frame, a pattern test, and a return of a boolean the
caller tested again.

## The rule

A leaf may end in an unreachable block, and the splice copies it as it
stands; a body with no return is still no leaf, as a `todo` stub is not
spliceable. The leaf and constructor-leaf limits count the blocks that
emit code (`code_blocks`): a block holding nothing but a jump is threaded
away by the SSA backend.

## Measured

x86-64 instructions, outputs identical:

| entry | before | this change |
|---|--:|--:|
| perceus_rbtree | 77,498,369 | 74,376,129 |
| perceus_rbtree_ck | 84,384,527 | 82,344,687 |

rbtree_ck's code grows 3% (7% on wasm), `is_red` twice over; the other
entries are unchanged. The compiler reproduces itself, is a little
smaller, and a sanitizer stage 1 compiles `checker.fern` clean.
`TestSelfHostExhaustiveLeafSplicedX86_64` fails before this change.
