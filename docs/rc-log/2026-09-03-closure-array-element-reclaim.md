# Closure arrays release their element boxes (#4354, residue 2)

The #4354 thread left three residues. This entry closes the second — closure
ARRAYS had no `__drop_arr_closure` equivalent on the self-host — and measures
the other two against HEAD so the issue can be re-scoped from numbers rather
than from its August status comment, which is stale: the `clo_rc_approved` /
`add_clo_cap_kinds` family it names was deleted on 2026-08-29 as unreachable
(`2026-08-29-clo-rc-inert-deletion.md`); only `"ENVCAP:"` survives.

## Measurements (x86-64, `FERN_LEAKCHECK=1`, 100 rounds, live_bytes at exit)

| shape | native | self-host before | self-host after |
|---|---|---|---|
| `[() => n, () => n+1, () => n*2]`, called by index | 0 | 12000 | **0** |
| same, captures a string and an `i32[]` | 0 | 12000 | 3200 (the string, see residue 1) |
| factory `return a` + `consume(fns)` with `var d = fns[0]` | 0 | 8000 | **0** |
| loop-local + `if`-block-local arrays | 0 | 10000 | **0** |
| `var d = fns[i % 2]` in a loop, `var e = fns[0]` | 12800 | 0 (see trap 2) | **0** |
| closure LOCALS as elements: `[c, …]`, `fns.append(c)`, producer `[c]` | 12800 | 8000 | **0** |
| `var fns = []` + `append(c)` + `append(lambda)` + string capture | 9600 | 7200 | 3200 (the string) |
| refusals: `return fns[1]`, `var g = fns`, `keep.append(f)` in foreach, `H { f: fns[0] }` | 6352 | 23960 | 23960 (sound, sanitizer clean) |

Every admitted row is 40 B per element per round — the env box — and every
"after" cell was re-run under `FERN_SANITIZE=1`: exit code unchanged, no
finding. The bump-fixpoint form (2000-round warm-up, second churn must not move
`__heap_bump_bytes()`, `__rc_underflow()` must read 0) exits 0 on x86-64, arm64
under qemu, and wasm under wasmtime.

## What landed

`"CLOARR:<site>"` — one more entry in the exit-sweep / loop-rebind credit
family, alongside `"ARRARR:"` whose release helper it reuses. An env box IS an
rc-headered array to `__fern_arrarr_free`: one rc-guarded `arr_dec` per element
then the buffer is exactly the closure-array drop, on all three backends, with
no new runtime helper. `slot_is_reclaimable_cloarr` routes both release sites
(`emit_dec_sweep_except_list`, and the bind-site rebind through
`emit_arrarr_reclaim_store`).

The credit is the safety argument, since the walk frees whatever the buffer
holds at rc==1 (`cloarr_unsafe_for`):

- every element that ever enters the buffer is OWNED by it — a lambda / its
  `__mkclo$` marker, or a closure local the literal and the self-append already
  retain as an is_arr ident. A closure-returning CALL is refused as an element:
  a callee can hand back a param's or a field's box uncounted.
- every read of the array is `fns[i](args)`, `var d = fns[i]`, `for f in fns`
  with `f` only ever called, `fns.len()`, or a bare argument at a borrowable
  position. Any other `fns[i]` is an uncounted element escape; any other method
  (`.with` / `.append` bound elsewhere) clones the buffer and shares its boxes.
- `"CAC:<fn>"` rows in `closurearr_ret_fns` (same list, no registry threaded —
  the `RCE:` / `AAC:` trick) admit a factory whose every return is a fresh
  literal or a once-bound local of one, so `var fns = mkfns(n)` takes the walk.

Two fixes the walk needed and that stand on their own:

1. `var d = fns[i]` (`lower_stmt_var_closure`) now retains the element and
   releases the slot's previous box through the cow-guarded `emit_arr_store`.
   Before, the bind stored the box uncounted and the slot's exit dec freed it
   out of the array — a use-after-free in waiting for any later `fns[i]()`, and
   the reason the "loop element bind" row read 0 above: `d` and `e` between
   them held exactly the two boxes, so the accident balanced.
2. `[c, …]` with a closure LOCAL first was classified an array-of-arrays by
   `arrarr_from_init_shape` (a closure local's slot is is_arr), which made
   `for f in fns` bind `f` as an OWNED array. Its exit dec double-released the
   last box with `var e = fns[1]`'s — at live_bytes 0, because the census counts
   frees, not owners. Only the quarantine sees it (exit 124, "touched a
   quarantined block"). The literal now declines the arr-of-arr mark when its
   first element is a closure box.

## Traps

- **The census reads an over-release as a CLEAN run.** Trap 2 above was
  invisible to `FERN_LEAKCHECK` on both sides of the fix; the sanitizer leg
  is what caught it. The new test runs every admitted case under both.
- `__rc_underflow()` is a self-host-only builtin — native rejects it with
  E001 — so a probe that wants a native exit-code oracle cannot carry it. The
  test's x86 cases use native's exit codes on probes WITHOUT it and put the
  underflow check in the bump-fixpoint program the wasm / arm64 legs run.
- The is_closurearr FLAG is not the credit's gate: an annotated empty literal
  grown by `.append` earns the flag at the append, which lowers AFTER the bind
  whose rebind release needs the verdict. Both release sites read the credit.

## What is left of #4354, measured

**Residue 1 — captured locals of an env-box closure.** With the box freed, the
captured local is what still leaks: `cloarr_str` 3200 B / 100 rounds is `nm`'s
string box, `cloarr_kinds` 14400 is a `string[]` + a struct + an enum payload.
Not a kinds problem any more (captures are borrowed, the box owns nothing) — it
is the `"STR:"` / struct / enum credits refusing a name a lambda captures:
`expr_unsafe_for_vb`'s `ast.ExprLambda` arm (irlower.fern, the
`astwalk.collect_idents_stmt` blanket capture test) flags any mention inside a
real lambda body. Forgiving it needs the captured local to provably outlive the
box (a loop-local string captured by a lambda appended to a function-scoped
array dangles otherwise), i.e. a scope relation between the capture site and the
`"CLOARR:"` site, threaded into the six-deep `body_unsafe_for` family the way
`alias_ok` is. Its own slice.

**Residue 3 — the escaping-closure drop thunk** is a both-compiler gap,
re-measured identical on HEAD: `var c = () => nm.len() + n; return apply(c)`
leaks 3200 / 100 rounds on native AND self-host; a factory `return c` with two
calls, 3200 on both; the bare alias `var d = c; d() + c()`, 3200 on both. A
parity item to track against #4451, not a port slice.

## Gates

All green on the commit (x86-64 host, arm64 under qemu, wasm under wasmtime):
`TestSelfHostClosureArrayRc{IRX86_64,WasmIR,IRArm64}` (new, 71 s), every
`TestSelfHost*Closure*` suite (50 tests, 419 s), `TestSelfHostLeakMatrixX86_64`
+ `TestSelfHostAllocCountMatrixX86_64` + `TestSelfHostRcPlanDiff` (69 s),
`TestSelfHostStage2FixpointArm64` (the exit-sweep credit gate, 130 s),
`go test ./internal/ir/ ./internal/lint/...`. On the parent commit the new x86
leg fails six cases in the leak direction (4000–12000 live bytes), the
foreach case in the sanitizer direction (exit 124, use-after-free), and the
fixpoint case with 98.

The lint ratchet is the one to watch: this tree's excess was 18416 before the
change against a recorded 17572 — 34 under the 5% ceiling — and the walkers add
27 after splitting, leaving 7. Main's drift, not this change, is what sits on
the ceiling; the recorded number wants banking.
