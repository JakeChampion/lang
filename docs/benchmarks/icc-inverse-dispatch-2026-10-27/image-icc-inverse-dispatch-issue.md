Parent: #13659 under #13216.

Add one standalone inverse entry point for public `tag_data.CurveData` values.
The API selects only the existing qualified inverse kernels without new arithmetic or model selection.
The current forward APIs and their unsupported forms remain unchanged.

Predeclared API policies:

- Use the existing `curv_eval.EvalLimits`, with default 4096 and unchanged sampled-table hard limit 65536.
- A nonpositive caller sample limit returns `Limit` before input or curve checks.
- A nonfinite input or input outside [0,1] returns `Input` before curve selection, with negative zero admitted.
- Identity, gamma, and sampled curves select `curv_inverse` with the original limits, flat policies, and error precedence after these common checks.
- Parametric function zero selects `power_inverse`, functions one/two select `affine_inverse`, and functions three/four select `quadratic_inverse`.
- Unknown parametric function numbers return `Function` without callbacks. There is no fallback to another kernel.
- Preserve every selected kernel's coefficient domain, numeric errors, cancellation count, COW behavior, and exact selected bits.
- Normalize existing errors into `Limit`, `Input`, `Function`, `Shape`, `Value`, `Domain`, `Unsupported`, `NonInvertible`, `Numeric`, and `Cancelled`.
- The dispatcher adds no arithmetic, callbacks, or storage of callbacks in cells.
- Fresh public records receive admission on every call. Owned metadata is not an inverse-eligibility or profile-conformance token.

Predeclared finite criteria:

- Retain every vector and independent mathematical expectation from the published curv, power, affine, and quadratic inverse reference bundles.
- The existing absolute-error criterion remains 1e-12, with every declared exact-bit control unchanged.
- Successful dispatcher values must match the corresponding standalone kernel bit for bit.
- Add explicit common-admission precedence, unknown-function, cancellation, unsupported-form, alias mutation, and fresh-call controls.
- Every admitted finite case must succeed. There is no Numeric waiver, seed filter, workload reduction, or tolerance change after a failure.
- Preserve immutable source/tool inventories and exact target sample equality across native, Wasm, and ARM64/QEMU10.
- Require normal fresh workers with nonzero balanced zero-live heap censuses and strict collector fault controls.

Acceptance:

- [ ] Verify the current kernels, common admission policy, and exact routing before implementation.
- [ ] Implement the standalone entry point without changing existing kernels, forward APIs, or model selection.
- [ ] Pass independent mathematical observations and exact standalone-result comparisons on all three targets.
- [ ] Qualify error precedence, all callback polls, COW, fresh calls, and meaningful collector fault controls.
- [ ] Reproduce the new reference bundle and preserve all original vectors and expected observations.
- [ ] Complete full target regressions, unchanged complexity gates, format, build, vet, selected lint, and Go oracle checks.
- [ ] Merge one READY PR normally at the exact full head and publish evidence without broader acceptance claims.

Primary reference: https://www.color.org/specifications/ICC.1-2022-05.pdf, Table 68 and Annex F.1/F.3.
No profile eligibility, inverse destination composition, adaptation, BPC, encoded admission, or runtime CMM dependency changes.
General fractional-power piecewise domains and discontinuous joins remain explicitly unsupported by the selected kernels.
All actual M5 and epic acceptance gates remain open.
