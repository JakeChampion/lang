# 2026-11-03 — a small branch splices, and keeps its slow callee out

A coreutils lead in #8278, from the `ls` rows of 4000 names.

## The shape

The sort in `ls` calls `before`, which calls `key_before` from two sites.
`key_before` has a fast path for a sort by name, and a slow path that calls
`cmp_key`:

```
function key_before(c: Cfg, es: Ent[], a: i32, b: i32): boolean {
  if (c.sort_key == sort_name()) {
    return es[a].name < es[b].name;
  }
  return cmp_key(c, es[a], es[b]) < 0;
}
```

`cmp_key` is called once, so the inliner spliced its 222 instructions into
`key_before`. `key_before` then had a full frame on its fast path, and it
stayed a call from the merge loop.

A body with two callers is spliced only when it returns a box, and `key_before` returns a boolean. Each comparison
paid a call and a frame of five saved registers for two loads and one
`__fern_str_cmp` call.

## The rule

- A small branch is a body of at most `max_shared_insts` instructions with
  two callers, no loop, and a scalar result. It joins the shared set,
  and each call to it is spliced, whatever the caller does with the result.
- A body called once stays out of a shared host when the splice grows the
  host past the host's own limit. The check runs in each round with the
  sizes of that round, because a callee grows as its own callees splice into
  it. The body stays out for the rest of the rounds.

A body with a loop is not a small branch. The sort itself has two callers
and returns no box. Spliced as a small branch, it kept `before` out of it.

| x86-64, `ls` of 4000 names | before | after | GNU 9.4 |
|---|--:|--:|--:|
| whole run | 13,170,043 | 12,148,766 | 11,298,193 |

The compiler that compiles itself takes the same time, 75.0 s against
76.4 s. Its peak memory is 0.8% more, and the compiler it makes is 1.3%
larger.

## Tests

`TestSelfHostInlineSmallBranch` builds a loop that calls a small `pick`
from two sites, and `pick` calls a large `slow` on its slow path. `count`
must not call `pick`, and it must call `slow`. The program must exit as the
interpreter does on every target. The test fails without the change.
