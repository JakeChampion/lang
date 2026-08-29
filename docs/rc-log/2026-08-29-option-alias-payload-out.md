# The alias that carries the payload out — an over-release the census hides

`4cf621d` gave the flat-array Option its consuming-match credit back when the
local is aliased, flipping `opt_arr__fnscope__alias_match` and taking the leak
matrix to no self-host-only leak on either architecture. The reasoning is
right and the row is right. One condition is missing, and it is a free-safety
one.

## The shape

```fern
var src: Option[i32[]] = Some([i, i + 1]);
var x: Option[i32[]] = src;
var out: i32[] = [0];
match (x)   { Some(xs) => { out = xs; }, None => {} }   // payload carried out
match (src) { Some(ys) => { … }, None => {} }           // source frees the box
```

Both oracles exit 68. The self-host exits **99** — `__rc_underflow_count()`
fired. `out` still holds the element buffer when the source's consuming-match
free releases it.

## Why the existing guard admits it

`opt_match_alias_escape_ok` asks three things of the alias: it is not
reassigned, it clears `body_unsafe_for_match_borrow`, and it is not read after
the match that frees the box. All three hold here.

The second is the one that matters. `body_unsafe_for_match_borrow` exists so
that an alias consumed by its own `match (x)` is not read as an escape, which
is correct — for the BOX. It says nothing about what the arm BINDS. `Some(xs)
=> out = xs` moves the element out of the frame while the box itself is only
borrowed, so the scan sees a borrow and the payload leaves anyway.

This is the same trap #7687 documented for the rc-enum family, and the reason
`rcenum_alias_bind_sites_of` pairs its escape scan with
`!enum_body_binds_rc_payload`. The Option guard needed the same second half.

The fix is one conjunct on the existing check —
`!opt_body_binds_rc_payload(body, v.name)` — not a second mechanism.

## Why nothing caught it

The over-releasing build's census **balances**:

| build | exit | census |
| --- | --- | --- |
| before the guard | **99** | `allocs=300 frees=300 live_bytes=0` |
| after the guard | 68 | `allocs=300 frees=200 live_bytes=4000` |

The broken build looks *cleaner* than the correct one — perfectly balanced,
zero live bytes — while the correct one carries an honest leak on a shape that
must stay refused. Every leak-accounting assertion passes on the unsafe build
and fails on the safe one, which is why the leak matrix, the census and a
`balance: true` test row are all blind here.

`4cf621d` said so itself: "a fix here is one wrong clause from a double free
and the leak checker cannot see one." Its conformance case pins both-matched,
alias-unused, a returned alias and two-fresh — four shapes that a leak count
CAN distinguish. The payload-out shape is the fifth, and it is the one that
needs the exit code.

**Rule: on any alias-forgiveness guard, assert the EXIT, not the balance.** A
row that pins `frees` is testing whether the credit was granted. Only the
underflow counter tests whether granting it was safe.

## Coverage

`TestSelfHostOptionAliasMatchConsumedX86_64` gates every row on the exit code
and the refused rows do not assert balance at all. Six shapes: the matrix
cell, a dead alias, an alias handed to a borrowing callee (refused today —
narrower than it could be, and safe), the payload-out shape, a returned alias,
and a reassigned alias.

Zero matrix rows move with the guard in place, on either architecture: it
refuses exactly the shape that over-releases and nothing else.
