# 2026-10-01 — a typed-lowering refusal is a compile error

Self-host (`semlower.substitution`, `semlower.runtime_bodies`). Step 2 of
"Retiring the AST lowering" in `../SELFHOST-SEMANTIC-SOURCE.md`.

With the typed path on, a program module the typed lowering does not produce
whole, or a runtime helper source it refuses, fails the compile: a
`FERN_SEM_IR: <name>: <reason>` line per refusal, then `FERN_SEM_IR: the
typed lowering refused …`, and exit 3. That was `FERN_SEM_IR_STRICT=1`'s
behaviour, and it had been the gate everywhere that compiled with the
self-host compiler (both e2e packages' `TestMain`, coreutils, the release
build and the four bench scripts) with no refusal in the compiler, the
examples, coreutils, conformance or the fuzz corpus. The variable is gone.

## What stays on the AST lowering

- `FERN_SEM_IR=` (the typed path off): everything, until step 3.
- The bisect knobs, `FERN_SEM_IR_ONLY` / `FERN_SEM_IR_SKIP`: a declaration
  they leave out goes through `ircore.produced_or_lowered` to `irlower`, as
  before, and the module compiles mixed. A runtime helper is selected by the
  same prefix test, so a knob that leaves one out (`ONLY` names no helper)
  gets `runtime_ast_bodies` for it rather than an error. Refusal lines print
  under a knob only with `FERN_SEM_IR_REPORT`, as before.
- An `@import` extern, which the substitution declines as bodyless, and a
  driver that supplies no `rt_lower`.

So the module path's only dead code was the `return ircore.no_sub()` after a
refusal; `produced_or_lowered` and `runtime_ast_bodies` still serve the three
cases above.

## What the default found

Compiles that passed a bare environment (`PATH` and one `FERN_` flag) never
saw strict, so they kept the AST lowering silently. Ten such compiles now
refuse, all one cause: the typed lowering routes a map onto core/map
(`ssarc.routed_map_site` calls `map_new_impl`), and a stdin driver
(`asm_ir_run`, `wasm_ir_run`) has no loader to bring core/map in, so it
reports `round: calls map_new_impl, which the AST lowering defines`. The AST
lowering used the runtime's own map, so these rows measured it rather than
the typed path:

- the five map cells of both leak matrices (`map_struct_*`, `map_strarr_*`);
- `TestSelfHostHeapBumpFlatIRX86_64`'s `map-get` case;
- `TestSelfHostMapStrArrColumnWasmIR` and `TestSelfHostMapStructColumnWasmIR`.

They compile through `asm_load_run` against the stdlib now (`loadCompile`),
the loading compile a map needs, and every recorded verdict holds: the
typed path's maps are clean on x86-64, arm64 and wasm. A sweep of the 134
map-touching tests in `internal/e2eselfhost` found no other refusal.

## Tests

- `TestSelfHostSemIRStrict`'s refusal cases compile with no `FERN_`
  variable at all (`childEnv`), and the four of them exit 3.
- `TestSelfHostStrictIRRefusesBail` lost its "without the flag it compiles on
  the AST lowering" leg, and `TestSelfHostStrictIRX86_64` its byte-equality
  leg against a strict-off compile; `TestSelfHostWasmModloadTypedLowering`
  lost the same off leg.
- `TestSelfHostSemIRRuntimeHelperRefusal` is new: no shipped helper is
  refused, so it builds `asm_run` from a copy whose `chr` source holds its
  block in an `i32`. With no variable set the compile exits 3 naming
  `__fern_chr`; under `FERN_SEM_IR_SKIP=__fern_chr` the helper is the AST
  lowering's.
- The semantic differential legs no longer read the module tally
  (`requireSemWhole` is deleted): a refused seed fails the compile, and
  `semRefused` turns that into a test failure rather than a skipped
  coverage gap.
