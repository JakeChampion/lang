# 2026-09-04 — the `?`-box gate's baseline caught up with it, so the ratio died

`TestSelfHostTryBoxReclaim{IRX86_64,IRArm64,WasmIR}` compared two churn loops
in one program: `match (mk(pre)) { … }` as the baseline and `mk(pre)?` as the
try path, asserting `gt + gt > gb + 256` — "the try path leaks at most half the
baseline". #7910 (d) (`9b4423842`, a producer-call scrutinee is a binding) gave
that baseline the same fresh-call-result reclaim the `?` path already had, so
`gb` collapsed onto `gt` and the comparator started failing on a compiler that
had not regressed.

## Measured

`__heap_bump_bytes()` deltas, N=1500, identical on x86-64 and arm64. One fixed
native compiler; only the `examples/self_host` sources varied between the two
driver builds.

| leg | tree | gb | gt |
|---|---|---|---|
| scalar | `00eacd3f3` | 120000 | 60000 |
| scalar | `9b4423842` and later | 60000 | 60000 |
| string | `00eacd3f3` | 168000 | 60032 |
| string | `9b4423842` and later | 60032 | 60000 |

`gt` never moved. The residual it holds is one box per round — the outer
`var r = innerT(pre)` box that the caller's own match still leaks, the
pre-existing class this gate was always written around.

## Pin

An absolute pin on `gt`, failing in EITHER direction (98 above, 96 below), the
same convention as `.github/cliff-baseline.txt` and the conformance leak census:
an improvement gets rebanked rather than silently absorbed.

| backend | rounds | box | pin |
|---|---|---|---|
| x86-64 (scalar, string, option-failure) | 3000 | 40 B | 120000 |
| arm64 (scalar, string) | 1500 | 40 B | 60000 |
| wasm (scalar, string) | 2000 | 16 B | 32000 |

Non-vacuity: forcing `try_box_fresh` false in `irlower.fern` (the flag gating
this reclaim) doubles every pin — 240000 / 120000 / 64000 — and every leg exits
98.

## Trap

A ratio between a fixed path and a moving one is not a pin. This one read as a
`?`-reclaim regression for a wave that never touched `lower_try`, and the bisect
had to run on the BASELINE half to see it. Where the quantity under test is a
leak count, pin the count.
