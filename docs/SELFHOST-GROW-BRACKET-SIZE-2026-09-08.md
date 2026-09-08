# Driver growth: preserve nested field roots

The match-expression typing fix remains intact. Its additional call edges
exposed an independent code-generation inefficiency: grow-containment retained
unrelated nested array fields whenever a callee could grow any named field.

`computeGrowParams` correctly reports that `checker.check_expr` and
`check_call_expr` can grow only the Scope fields `names` and `types`. However,
`arrayFieldPaths` discarded the root field name when descending into a nested
struct. `growBracketArgs` therefore could not filter those paths and bracketed
the arrays inside the unrelated signature, struct, union and method tables.

Each path now retains its root field separately from its direct-field death
name. A precise growth summary filters unrelated subtrees; an unresolved `*`
summary still protects all paths. Nested paths still have no direct-field death
verdict. Existing lifetime, alias, own-argument and copy-on-write rules remain
unchanged. No inference or typing pass is removed.

## Same-source A/B measurements

Control: clean `c2f7057ea`. Candidate: that revision plus the IR change only.
Both use each driver's actual import closure, the Go compiler and the normal
CI linker via `TestSelfHostWarmStockDriver`, with no disk build cache. Every
linked binary also passes the harness's QEMU smoke execution.

| Driver | Control bytes | Candidate bytes |
|---|---:|---:|
| fern.fern | 13,268,412 | 11,453,884 |
| asm_load_run.fern | 10,032,884 | 8,218,356 |
| asm_modload_run.fern | 9,037,316 | 7,583,236 |
| asm_ir_run.fern | 8,798,156 | 7,352,268 |
| asm_run.fern | 8,322,300 | 6,909,180 |
| wasm_ir_run.fern | 8,372,060 | 6,958,940 |
| wasm_run.fern | 8,347,148 | 6,938,124 |
| wasm_runio_run.fern | 8,313,452 | 6,904,428 |
| asm_pathprobe_run.fern | 7,587,052 | 6,173,932 |
| ssa_lift_scan_run.fern | 7,396,900 | 5,987,876 |
| asm_ir_elig_run.fern | 7,074,156 | 5,661,036 |
| irlower_run.fern | 7,395,788 | 5,982,668 |
| checker_modload_run.fern | 2,481,156 | 2,141,188 |
| ssa_emit_run.fern | 1,731,692 | 1,731,692 |
| ssa_run.fern | 1,553,596 | 1,553,596 |

The full compiler shrinks by 1,814,528 bytes, the checker driver by 339,968.
This is a general code-generation improvement, not an attribution of all
current growth to the bisected typing fix. All 13 changed baselines are lowered;
the two unaffected baselines remain unchanged. No threshold is increased.

Before/after IR diagnostics corroborate the mechanism: `checker.check_expr`
falls from 325 retains and 280 releases to 73 and 28, while `check_call_expr`
falls from 208 and 215 to 64 and 71. The growth summaries remain `names,types`.
These are static instruction counts, not runtime execution counts.

Environment: native ARM64 Linux in `lang-pr-ubuntu-cross:24.04`, with x86-64
cross tools and QEMU. These are deterministic linked-size measurements, not
native x86-64 runtime performance claims. The three-driver pilot and the full
15-driver candidate run agree exactly on their overlapping measurements.

Reproduction, with the checkout mounted at `/work` and shared Go caches:

```sh
driver_growth_names=$(awk '/^[a-z_]+\.fern/ {if (n++) printf ","; printf "%s", $1}' .github/selfhost-driver-sizes.txt)
FERN_REQUIRE_X86_64_TOOLING=1 FERN_WARM_DRIVER="$driver_growth_names" \
  go test ./internal/e2eselfhost -run '^TestSelfHostWarmStockDriver$' \
  -count=1 -v -timeout=2m
```

Candidate report: [all 15 sizes](benchmarks/selfhost-grow-bracket-sizes-2026-09-08.txt).

## Regression coverage

The IR test fails before the fix at both pointer widths: growing one direct
array brackets three arrays instead of one. The corrected test also preserves
all three brackets for unresolved nested growth and the two appropriate
subtree brackets when forwarding a struct field.

Runtime tests independently require unchanged caller values and correct grown
results on interpreter, x86-64, ARM64 and Wasm. Existing borrowed-array append,
nested growth, transitive forwarding, cursor rename and loop-rebind controls
also pass on x86-64 and ARM64. The complete IR, checker, parser and source-lint
suites and `make lint-all` pass. The full `go test ./... -timeout=60m` suite
also passes in the native ARM64 Linux validation container. Dedicated cross-
toolchain pilots above cover x86-64 execution separately; QEMU results are
correctness evidence, not native timing evidence.
