# A builtin lends what its contract lends

2026-09-24. Self-host, ownership inference.

```
function push_range_or_filler(h: usize, l: Line, f0: i32, f1: i32, o: Opts): void {
  if (f1 > f0) { buf_push_range(h, l.text, f0, f1); return; }
  buf_push(h, o.filler);
}
```

`coreutils/join.fern` retained `l` and `o` at every call and released
one of them on each path inside, for a function that only reads both.

## Cause

`ownership.consumed` answers "yes" for a symbol with no row, and a
builtin had none. `l.text` handed to `buf_push_range` therefore counted
as leaving the frame, and `l` was carried with it. The builtin's
contract says the slot is lent, and `ssarc` lowers the call that way;
only the inference ignored it.

Each builtin now has a fixed row, read off its contract: a slot is
consumed where the contract counts it. The rounds never revise it.

`lent_values` still marks a lent argument as lent, since a callee may
keep a borrow of it in what it returns, and a lent-marked parameter is
never counted. A builtin whose result holds no reference cannot do
that, so its row says `lends: false` and its lent slots mark nothing.
Without that, `join`'s `next_line`, which reads `inp.buf` through
`__memchr` and returns `In { ...inp, ... }`, would turn borrowed and
lose its in-place rebuild.

## Measured

Self-host x86-64 builds, instructions:

| workload | before | after |
|---|---:|---:|
| `join` of two 300k-line files | 404.3M | 363.2M |
| `cat -n` of 1M lines | 316.9M | 315.9M |

`uniq`, `sort`, `ptx`, `wc`, `tr` and `fmt` did not move. Outputs were
byte-identical.

`TestSelfHostOwnershipInference`'s emitted-modes check gains a record
read only through a builtin's lent slot (`lends_to_builtin`, no
`__sem_drop_Rec`), next to one that hands its record back on one arm
and drops it on the other (`keep_or_new`, which must).
