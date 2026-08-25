# Matching an enum is not handing it out — killer-drops slice 12

An enum local that is not matched exactly ONCE at top level reclaimed nothing.
Matched twice, matched inside an `if`, matched inside a loop — all **200 allocs
/ 0 frees** over 100 rounds against native's 200/200.

That is not a corner. Matching in a conditional, matching in a loop, and
matching the same value twice are ordinary Fern; the compiler's own sources do
all three constantly.

## Why the shape mattered

`collect_fresh_rcenum_names` has two branches. The first takes over when
`sole_top_level_match_idx` finds exactly ONE top-level match on the name — that
match consumes the value, and the credit is `"RCENUM:"` alone. Everything else
falls to the second branch, gated only by `body_unsafe_for_enumfield`.

`ef_unsafe_stmt`'s StmtMatch arm sent its scrutinee to `ef_unsafe_expr`, whose
default arm reaches the strict walker, which reads a bare ident as an escape. So
the second branch refused every name it was asked about.

`stmt_unsafe_for_match_borrow` has read a bare-ident scrutinee as a BORROW all
along. This fork — a thin fork of the strict walker, added for the enum-field
carve-out — never picked that reading up.

## The soundness question, and the answer

Matching a value does not hand it out. What can hand it out is an arm BINDING
its payload (`E.A(xs) => { keep = xs; }`), and on this branch nothing refuses
that: the arm body mentions `xs`, not the matched name, so the enum walk sees
nothing.

It is sound anyway, and for a reason worth stating rather than assuming: the
binding's own assignment RETAINS. Two counted owners, so the sweep's dec leaves
`keep` holding one.

Measured both ways, because one of them cannot see the failure:

| shape | before | after | native |
|---|---|---|---|
| non-sole match, arm binds the payload out | 300/100 | **300/300** | 300/300 |
| same, payload read back after three fresh arrays | value 53 | value **53** | 53 |

The second row is the one that matters. Counts and `__rc_underflow_count()`
cannot see a use-after-READ — the arrstruct live-element slice
(`2026-08-25-field-reclaim-shared-box.md`) shipped one that passed both, and
`FERN_SANITIZE=1` reported only a leak. So the churn row asks for the VALUE with
three fresh arrays allocated after the match, and native's answer to compare
against. Sanitizer clean too, and all three backends agree.

(The probe's modulus is 97: the wasm leg reads the value through the exit code,
and WASI rejects a status outside [0, 126). The first cut used 251 and the wasm
leg failed on that alone — worth knowing before writing the next value probe.)

## Results

| shape | before | after | native |
|---|---|---|---|
| matched twice at top level | 200/0 | **200/200** | 200/200 |
| matched inside an `if` | 200/0 | **200/200** | 200/200 |
| matched inside a `while` | 200/0 | **200/200** | 200/200 |
| non-sole match binding the payload out | 300/100 | **300/300** | 300/300 |
| inline-ctor sole match (other branch) | 200/200 | 200/200 | 200/200 |

All 14 rc regression probes unchanged. Pinned across x86 / arm64 / wasm by
`internal/e2eselfhost/self_host_enum_match_scrutinee_borrow_test.go`.

## Two gaps this did NOT close, pinned as gaps

- **A SOLE top-level match that binds its payload out** stays at 300/100 against
  native's 300/300. That shape takes the other branch, where
  `match_arm_binds_rc_payload` refuses it. Pinned as the gap it is.
- **A CALL-bound enum with a sole top-level match** — `var v: E = mkv(i)` — is
  200/0 against native's 200/200, while the identical shape bound from an INLINE
  ctor is 200/200. Measured before and after this slice: unchanged both times,
  so it is genuinely separate. The match-consumed branch admits the call bind
  (slice 4's `rcenum_call_init_owner` / the `"RCE:"` registry) but the release
  never fires; `consumed_rcpayload_enum_frees` is where to look.
