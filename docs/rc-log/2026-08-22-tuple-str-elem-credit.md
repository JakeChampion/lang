# The string tuple element's credit, granted

#7226's string limb, the half left open when the array limb landed — and the
larger leak of the two: **32 B/round unbounded**, against the array's 40 bounded.

| shape (100 rounds, x86-64) | native | before | after |
| --- | --- | --- | --- |
| `var s = w("ab"); var t: (i32, string) = (i, s)` | `live=0` | `300/100` **3200** | `300/300` **0** |
| the same at 200 / 400 rounds | `live=0` | **6400** / **12800** | **0** / **0** |
| two string elements | `live=0` | `500/100` **6400** | `500/500` **0** |
| a string element AND an array element | `live=0` | `400/200` **3200** | `400/400` **0** |
| the source local read after the tuple | `live=0` | `300/100` **3200** | `300/300` **0** |
| the precise drop claims the box, not the sweep | `live=0` | `300/100` **3200** | `300/300` **0** |
| the tuple is a cross-tuple reuse DONOR | `live=0` | `500/100` **6400** | `500/500` **0** |

Exactly 2.0× per doubling: unbounded, not a constant.

## Measure it with `w("ab")`, never `"ab" + "c"`

The issue's original table called this row clean. That row used `var s = "ab" +
"c"`, which constant-folds to an **immortal literal** (`constfold.fern:209`, box
rc = -1) — the probe measured a constant and reported it as flat. Every figure
above goes through a `function w(a: string): string { return a + "!"; }` call, and
the test asserts `allocs != 0` so the mistake cannot recur silently.

## It was the CREDIT, not a missing release

`lower_expr`'s ExprTuple arm already retained the element: `slot_is_rc_container`
includes a string slot, so the `__fern_rc_inc` fired all along. What did not
happen is the string local's own sweep — `expr_unsafe_for`'s ident arm
escape-flags a bare-ident tuple element, so the local never earned `"STR:"` and
its box was never swept. **incs 1, decs 0.**

That is why the array limb's fix could not simply be widened. Adding the element
release alone balances the tuple's reference and leaves the local's box at 1
forever; granting the credit alone leaves the tuple holding a reference to a box
the sweep just freed. Neither half is useful, or safe, without the other.

The credit comes from the interlock `body_unsafe_for_clo` already ran twice — for
closure captures (#4354) and for union payloads inside an rc-tuple (#4353). A
third entry, same shape:

- `reclaimable_names_of` seeds `clo_ok` with `"TUPE:<name>"` for every tuple it
  credited `"TUPELEMOK:"`;
- `body_unsafe_for_clo`'s ExprTuple arm stops flagging a name that
  `tuple_bare_ident_sole_use` proves appears ONLY as a bare-ident element there.

Keying on `"TUPELEMOK:"` rather than on `"TUP:"` is what makes the pair
consistent. That credit already requires a tuple annotation, non-escape, and
`rctuple_payload_escapes` — so where it holds, the element release IS emitted, and
where it does not, the interlock stands down with it. The two can only be granted
or denied together.

## `__fern_rc_dec` on a string element is heap corruption, not a leak

A string box is `{rc@base, data@base+8, len@base+16}` with the value at `base+8`.
So `rc` sits at `value-8` for **both** layouts, which is exactly why one
`__fern_rc_inc` retains an array buffer or a string box indifferently — and it is
what makes the wrong release look plausible. But `__fern_rc_dec` frees at
`value-16`, the arr-box start; for a string that is eight bytes *below* the block.

The recorded kind therefore picks the helper: `a` → `__fern_rc_dec`, `s` →
`__fern_str_free`, `.` → nothing. The mixed-element row above is the case a single
helper for the whole walk gets wrong, and it gets it wrong in the direction that
corrupts the heap rather than leaking.

## What is deliberately refused, and measured as still leaking

Each of these keeps its pre-change number with the underflow counter at 0 — a
leak, and the safe direction:

| shape | why | live |
| --- | --- | --- |
| `var u: string = t.1` | `rctuple_payload_escapes`: `string` is not a scalar type name, so a bare extraction denies `"TUPELEMOK:"` — and with it the interlock | 3200 |
| `return t.1` | the same gate, leaving the frame | 3200 |
| `(s, s.len())` | `tuple_bare_ident_sole_use` refuses a tuple that mentions the name anywhere but the element position | 7200 |
| a `.trim()` VIEW element | recorded `.`, see below | contract-only |

A **view** slot is recorded `.` even though the construction retained it. Its own
sweep frees it with `__fern_str_view_free`, which frees the view BOX rather than
decrementing a shared buffer, so a second claim here would free a box the local
still owns. The tuple's reference to a view leaks; that is the only string shape
this does not close.

That branch is **contract-only, not witnessed.** The shape that reaches it —
`var s: string = raw.trim()` — is an E003 on native and interp alike (`str` is a
borrowed view; the hint says add `.to_owned()`), so there is no program that both
oracles will compile and that lands a view in a `string` slot. The self-host
checker *does* accept it, which is #7293.

## Two leaks found alongside, neither this path

- **A loop- or block-scoped fresh string local is swept by nothing** — #7292.
  `while (…) { var s = w("ab"); acc = acc + s.len(); }` measures `600/400`, 3200
  over 100 rounds with **no tuple anywhere**. This is the floor a tuple wrapping
  such a local lands on: the loop-rebind row went 9600 → 3200, and 3200 is this.
  The untaken-branch case in the new suite sits on the same floor, which is why it
  asserts the answer rather than the byte count.
- **The self-host checker accepts `var s: string = raw.trim()`** — #7293, found
  while trying to build the view probe above.

## Non-vacuity, and where it does NOT hold

Six of the seven cases in
`internal/e2eselfhost/self_host_tuple_str_elem_retain_test.go` fail with the
change reverted and the compiler rebuilt. The seventh, `litstr_elem`, was already
balanced and pins nothing about the leak — it is kept for the other direction,
since that class now records kind `s` too.

Only the **x86-64** leg carries that signal. The wasm and arm64 legs assert exit
codes, which a leak does not move, so they pass either way; what they catch is a
release that frees a LIVE box on those backends, which does change the answer.
