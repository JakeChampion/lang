# Semantic production pipeline: compiler-size attribution

Measured on 2026-09-15 at `4c006c860`, against the last baseline refresh
`5f4cb2400`. The CLI now includes the typed semantic producer and ownership
lowering introduced by #9321. That implementation is reachable through
`FERN_SEM_IR`, including when the environment flag is unset at build time.

## Exact linked sizes

| Build | Bytes |
| --- | ---: |
| Baseline revision, rebuilt | 12,048,860 |
| Current revision | 12,881,996 |
| Current revision with only `cli_substitution` returning `ircore.no_sub()` | 12,317,724 |
| Semantic pipeline linkage, same-source difference | 564,272 |
| Other growth since the baseline | 268,864 |

The baseline rebuild reproduces the checked-in number exactly. The current
build is 833,136 bytes larger (6.91%). Removing the producer's call edges
in a temporary source copy removes 564,272 bytes. This is an attribution
experiment: the reduced compiler cannot select semantic lowering and is not
a replacement artifact or a proposed optimization. The remaining 268,864
bytes are 2.23% of the old baseline, within the existing 5% allowance.

These are CI harness artifacts from `cachedDriverBin`, with the normal
in-process x86-64 emitter and ELF linker. The independent stock-driver run
on Linux ARM64 agrees with the macOS ARM64 in-process build and passes its
QEMU smoke execution. QEMU supplies correctness evidence, not timing data.
The native CLI's own build command produces a different artifact; its size
is not mixed into this comparison.

## Avoidable linkage was removed before this refresh

The first production integration called the semantic producer from each
backend entry point. That made partial drivers link the pipeline even
though they cannot select it. Commit `e42b99e05` moved the producer to the
CLI and passed an `ircore.Sub` value into each backend. This repair is
described in [the production consumer](SELFHOST-SEMANTIC-SOURCE.md#the-production-consumer).
The current call graph retains that boundary: only `cli_substitution`
calls the producer from the CLI, and backend entry points accept the value.

The current full fifteen-driver measurement and smoke run passes. Each of
the fourteen partial drivers remains inside its existing baseline tolerance;
their baselines are not refreshed. The CLI's new linked functionality is the
reason to record its measured size. The gate, tolerance and completeness
requirement remain unchanged.

This does not claim that the compiler has no further size optimization
opportunities. It separates the new implementation's linkage from the
remaining growth and retains the existing gate for subsequent changes.

## Where the text grew

GNU `as` and `nm -S` on the matching emitted assemblies give these function
text differences. This is a module attribution, not a claim that every byte
in a module came from one commit.

| Module or group | Text growth, bytes |
| --- | ---: |
| semsource | 297,236 |
| ssarc | 91,737 |
| ssasem | 59,244 |
| ssaunits | 33,244 |
| ssadeps, semlower, ssalive, ssalayout, semtypes, semrecords | 58,650 |
| irlower | 165,193 |
| parser | 23,785 |
| Remaining functions, net | 71,833 |
| Total function text growth | 800,922 |

The baseline function text totals 11,447,492 bytes and the current text
12,248,414 bytes. The remaining 32,214 bytes of file growth are outside
these function sizes, including data and layout. The same-source linkage
experiment above measures the whole file, so it includes those effects.

The semantic modules implement typed source production, value layouts,
dependency and liveness analysis, ownership lowering, and the production
substitution. The interval also adds self-host lowering for `@try`, directory
entries, CRC32 and scheduling priority, plus correctness repairs. The module
table reports accumulated changes without assigning all of them to #9321.

## Reproduction

The complete stock report is checked in beside this document:
[all fifteen driver sizes](benchmarks/selfhost-semantic-production-sizes-2026-09-15.txt).
From each revision, run the existing stock harness with all baseline names:

```sh
driver_names=$(awk '/^[a-z_]+\.fern/ {if (n++) printf ","; printf "%s", $1}' .github/selfhost-driver-sizes.txt)
FERN_REQUIRE_X86_64_TOOLING=1 FERN_WARM_DRIVER="$driver_names" \
  go test ./internal/e2eselfhost -run '^TestSelfHostWarmStockDriver$' -count=1 -v
```

For the same-source attribution, copy `examples/self_host/*.fern` into a
temporary project with `fullSelfHostProject(t)`. Replace only the body of
`cli_substitution` in that copy with `return ircore.no_sub();`, then build
`fern.fern` with `cachedDriverBin(t, "", dir, "fern.fern")` and stat the
returned path. Leave the imports, remaining source, Go compiler and linker
unchanged. Restore no files in the real checkout: the experiment edits only
the temporary project. Its import-closure cache key changes with that source.
