# The checker annotates without preparing again

`checked_module` prepares its module: it fills the default arguments, settles the literal locals, pretypes the module and qualifies the variants.
Since the change before this one, it also annotates the module once for the build.
It used `annotate_module` for that, which qualifies the variants, settles the literal locals and pretypes the module again before it annotates.

`annotate_prepared` now holds the annotation loop.
`annotate_module_in` prepares the module and then calls it, and `checked_module` calls it directly.

## Measurement

The subject is the fixed `compiler/checker.fern` and its imports from the commit before this change.
Each compiler was built by itself from its own tree for x86-64, without `-g`.
Each compile ran under Callgrind 3.22 with `--cache-sim=no --branch-sim=no` and a separate primed proof cache.

| compiler | Ir |
| --- | --: |
| before | 28,781,184,940 |
| this change | 28,689,168,345 (−0.32%) |

The two compilers emit a byte-identical x86-64 executable, and byte-identical arm64 and wasm assembly, for the subject.
