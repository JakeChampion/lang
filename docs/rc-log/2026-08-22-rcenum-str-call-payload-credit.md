# The string payload that came from a function, credited

#7364, found while building #7360's controls: `R.Full(w("x"))` in a conditional
block — the payload factored through a user function — left the enum with no
reclaim credit at all.

```fern
enum R { Full(string), Empty }
function w(a: string): string { return a + "!"; }
if (i % 2 == 0) { var o: R = R.Full(w("x")); t = t + 1; }
```

| shape | self-host before | after | native |
| --- | --- | --- | --- |
| exit-sweep (if-block local) | `150/0` **3600** | `150/150` 0 | `50/50` 0 |
| match-consumed (top level) | `300/0` **7200** | `300/300` 0 | `100/100` 0 |
| rebind (`o = R.Full(w("yz"))`) | `300/0` **7200** | `300/300` 0 | `100/100` 0 |

The 3-vs-1 alloc ratio against native is #7351's per-string divergence, not this
change. The byte-identical program with the concat INLINED (`R.Full("x" + "!")`)
was already balanced, so factoring the payload through a function was the entire
difference — and `frees=0` (not n-1-of-n) said the whole credit was missing, not
one release site.

## The registry already existed

`variant_struct_payloads_fresh`'s string arm accepted a literal, a concat, a
named builtin, or a string method — `str_local_binding_is_fresh`, deliberately
syntactic, refusing every general call because *a call may return a borrowed
param*. The refusal is right; the blindness was not: `str_fresh_ret_fns` — the
whole-program least fixpoint of free functions whose every return is a fresh
sole-owned string box — has existed since the string-binding reclaim and is
consulted by every other owner of a fresh-string verdict. The gate just never
took it.

So the fix is a parameter, not a new analysis: `str_fresh` threads from
`reclaimable_names_of` (which already received it) into
`collect_fresh_rcenum_names` → `all_assigns_fresh_rcenum` →
`fresh_rcpayload_enum_init` → `variant_struct_payloads_fresh`, and likewise into
`consumed_rcpayload_enum_frees` and `precise_drop_names` from their callers'
sig-groups. A bare-ident callee in the registry is fresh; everything else falls
through to the syntactic set unchanged.

Three sites keep the strict `[]`: struct-literal enum fields
(`struct_lit_all_enum_fields_fresh`), array-of-enum elements
(`arrenum_lit_enum`), and the `"RCE:"` registration proof
(`body_has_nonqualifying_rcenum_return`) — the last because it runs while
`opt_fresh_ret_fns_of` is being built, and keeping it syntactic avoids ordering
that registry against `str_fresh_ret_fns_of`. All three refusals cost a leak,
never an over-release.

## What is still refused, and the probe that was invalid

`refused_alias_returning_callee` — `function id(a: string): string { return a; }`
as the payload producer — stays uncredited end to end: `id` never enters the
registry (a bare-ident return is refused by the fixpoint), so the enum keeps its
sound leak, `250/0` with underflow 0 and both oracles at exit 52. MORE frees on
that row is the over-release this gate exists for.

A second refusal probe — a callee returning `a.trim()` — turned out to be
**invalid Fern**: `trim()` yields a `str` view, native and interp both reject
the program (E003), and only the self-host checker accepts it. That is #7293,
not a reclaim shape; the probe was dropped rather than pinned.

## The pin that moved, and where it is pinned now

#7360's suite pinned the leak as `string_payload_still_leaks` (`150/0`), named
so this change would move it deliberately. It now measures `150/150`, is renamed
`string_payload_swept`, and pins the other direction — the row that fails if the
credit is ever withdrawn. Same rename the log records for
`producer_local_now_credited`.

## One measurement trap worth keeping

The alias-refusal probe was first written with `var keep: string = "base" + "!"`
and its counts read off the CLI build: `50/0`. The e2e harness then measured
`250/0` on the same program — the CLI **const-folds** a literal-literal concat,
the test driver does not, so a probe whose strings can fold pins different
numbers in the two loops. The probe now builds `keep` through the registered
producer (`w("base")`), which neither path folds; counts were re-read from the
harness, not the CLI.

## Non-vacuity

`internal/e2eselfhost/self_host_rcenum_str_call_payload_test.go`, 5 cases × 3
backends. Reverting the irlower threading fails the three admitted rows at
exactly their base measurements (`150/0`, `300/0`, `300/0`); the literal control
and the alias refusal pass either way — which is what a fix that worked by
widening indiscriminately would fail.
