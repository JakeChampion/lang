# The return-path drop was armed over one statement, not the local's live range

#7742. #7725 gave the return-path sweep the *right release*; this gives it the
right *window*. `optret_pending` was installed immediately before the candidate's
consuming match lowered and restored immediately after, so a `return` from any
earlier statement carried an empty pending set — and the post-match drop it
jumped is the local's only release, because the consuming-match analysis owns the
name and no exit-sweep class covers it.

No alias, no nesting, no arm:

```fern
var src: Option[i32[]] = Some([i, i + 1]);
if (i >= 0) { return 5; }
match (src) { Some(b) => { return b.len(); }, None => { return 2; } }
```

## Measured

x86-64, `FERN_LEAKCHECK=1`, `bin/fern -interp` and the native x86-64 backend
agreeing on every exit code.

| shape | rounds | native | before | after |
| --- | --- | --- | --- | --- |
| `Option[i32[]]`, early return | 100 | 200/200/0 | **200/0 live 8000** | 200/200/0 |
| same | 400 | 800/800/0 | **800/0 live 32000** | 800/800/0 |
| rc-enum `E.Full(i32[])`, same | 100 | 200/200/0 | **200/0 live 8000** | 200/200/0 |
| scalar enum `S.A(i32)`, same | 100 | 100/100/0 | **100/0** | 100/100/0 |
| `Option[P { xs: i32[] }]`, same | 100 | 300/300/0 | **300/0** | 300/300/0 |
| `Option[string]`, same | 100 | 100/100/0 | 300/300/0 | 300/300/0 |
| return AFTER the match (control) | 100 | 200/200/0 | 200/200/0 | 200/200/0 |

80 B/round and unbounded on the array row — `frees=0`, nothing released at all.
The flat `Option[string]` row is clean on both sides for the reason #7725
recorded: it carries a slot credit whose exit sweep still runs on the return
path, so the exposure is precisely the shapes whose sole release is the
consuming-match drop.

## The fix is the condition, not a new mechanism

The install block already runs once per statement and is restored after it. It
asked `idxs[k] == i`; it now asks `vidxs[k] < i && i <= idxs[k]` — the
candidate's live range, from after its `var` through its match. `vidxs` is the
declaring statement's index, which every one of the three builders already had
in hand as its loop variable.

`i == vidxs[k]` is excluded deliberately: the slot is not bound until that
statement finishes, and nothing can strand a value that does not exist yet.

**One thing that looks accidental and is load-bearing.** The rc-enum entry's
moved set is `match_moved_rc_payloads(stmts[i], …)` — the CURRENT statement.
Before the match that answers "nothing moved", so the entry carries the full deep
drop, which is right: no arm has bound anything on that path. At the match it
answers with the arm's own moved set. Passing the match's set on a pre-match
return would withhold drops for fields that path never moved.

## Why exactly one release fires

The two sites are alternatives on disjoint paths, which the widened window does
not change:

- a `return` inside the range emits the drop and leaves the function; the
  post-match drop is dead code on that path;
- no return, and the post-match drop runs while the pending entry is restored
  away before the next statement.

`early_return_conditional` runs both in one program (the return is taken on half
the rounds) and balances, which is the property stated as a measurement rather
than an argument.

## Gate

`TestSelfHostEarlyReturnConsumingDrop{X86_64,WasmIR,IRArm64}` — ten rows, each
with a second leg under `FERN_SANITIZE=1`. Two round counts on the array row,
because the discriminator between this and a bounded leak is whether
`live_bytes` moves with the count and a single count cannot show it.

## What this also closed

The two leak-matrix rows #7687 opened on —
`enum_rc_payload__fnscope__alias_match` and `opt_arr__fnscope__alias_match` — in
their `return`-carrying form. They were never an alias defect: the alias was
incidental and the `return` was the variable. Both balance now.
