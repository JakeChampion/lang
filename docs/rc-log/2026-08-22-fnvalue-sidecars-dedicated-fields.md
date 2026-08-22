# The fn-value sidecars get fields, and LowerState crosses 34

#7253 step 2, slice 3. Byte-identical output on BOTH register asm and wasm
wat for a probe exercising all three sidecars — an i64-width fn param, a
fn-typed local rebound from it, and a dyn-position fn value — which matters
here because two of the three exist *for* wasm funcref validation.

`"FNDYN:"` / `"FNSIG:"` / `"FNRET:"` were three prefixes spelling one concept:
the signature sidecar of a fn-typed value that the flat `"fn"` type tag
dropped — dyn-boxed argument positions (#5276), the funcref width tag wasm
validates against (#6282), and the declared return spelling `lower_i64`
widens by (#6862). They move to three dedicated fields (`fn_dyn` / `fn_sig` /
`fn_ret`), seeded by `lower_func`'s existing pre-pass loops for fn-typed
params and locals; the three readers take empty-prefix lookups over their own
small lists instead of scanning the whole multiplexed table per call site.

That takes `LowerState` to **35 fields** — past the deleted backend's 34-field
miscompile line for the first time — so the stage-2 fixpoint and the selfhost
CI shards compiling this struct through the self-host backends are now the
standing empirical witness that nothing else breaks above 33. The slice-1
entry could only argue the pin's *reason* was dead; this one crosses the line
it drew.

Family status after three slices: `CLO:` (name-keyed, conversion pending),
`TUPELEM:` (done), `FNDYN:`/`FNSIG:`/`FNRET:` (done as fields; still
name-keyed by design — a call site resolves its callee by name, and the
shadow suites `fn_param_shadows_module_fn` / `fnvalue_param_shadow` pin the
hazard's known edges). Five namespaces out of the 73 retired.

Gates: the six fn-value suites (cross-unit, dyn-fn coerce/param, shadow ×2,
capture, wide-sig wasm, x86+generic fnvalue), the closure suites, the leak
matrix — 122 s total, 0 failures; the double byte-diff; gen1 build. Fixpoint
to CI.
