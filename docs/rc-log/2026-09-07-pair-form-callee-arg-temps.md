# A fresh temp handed to a pair-form callee is released after the call

`TestX86_64CertifyAgreesWithTheLeakCensus` went red when #8789 banked
`stdin_double` and `multiline_stdin` at zero: both had leaked their
`read_line()` box, so the walk's finding on them had never been compared
against the oracle. The finding was the `trim()` result — `__str_slice`,
fresh, origin `call` — handed straight to `parse_int` and never released.
The walk was right and the census could not see it: "21" trims to an
inline string, and an inline string is not an allocation.

## What was wrong

Stage-(b) argument-temp reclaim refused every pair-form callee outright
(`!b.pairForm[id.Name]`), so a fresh temp passed to `parse_int` — or to
any user function returning `Option[i32]` — was owned by nobody. The
refusal was about the operand stack after the call, but the drops are
stack-neutral, and the enum spelling alone was never the right question:
a pair-form `Option[string]` callee CAN hand the argument back (`Some(s)`),
a pair-form `Option[i32]` callee cannot.

Admitted now when every variant's payload is a concrete scalar
(`pairPayloadsCannotAliasArg`). The guarded drops, which read the result,
stay unadmitted for a pair-form callee.

## Measured (x86-64, `FERN_LEAKCHECK=1`, 50 rounds)

| shape | before | after |
| --- | --- | --- |
| `line.trim().parse_int()`, 14-byte line | `allocs=100 frees=50 live_bytes=1600` | `allocs=100 frees=100 live_bytes=0` |
| `classify(mk(i))`, `classify: string -> Option[i32]` | `allocs=50 frees=0 live_bytes=1600` | `allocs=50 frees=50 live_bytes=0` |

Census rows that moved with it: `closure_field_match` 600 → 550,
`http_cookies` 9 → 8. Corpus case `pair_form_callee_arg_temp_released`
gates both shapes on all three backends.

## Trap

On wasm (two-word strings) the same programs read identically with and
without the fix: a `str` there is a bare (ptr, len) view with no header, so
the leak is native-only. Measure the natives for anything `str`-shaped.
