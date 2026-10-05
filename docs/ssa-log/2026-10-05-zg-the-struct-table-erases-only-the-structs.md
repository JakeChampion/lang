# 2026-10-05 — the struct table erases only the structs

Self-host typed lowering, every target. Refs #8171.

## The shape

`semlower.substitution_of` builds the struct table the lowering reads from
the module's struct declarations, with view types erased. It got them by
erasing the whole module, `parser.erase_view_module(im)`, and reading
`.structs` off the result. That erasure walks every function body as well, to
find view types to rewrite, and threw the bodies away. `verdict` and
`verdict_annotated` built their struct tables the same way.

`parser.erase_view_structs(im.structs)` is the part of `erase_view_module`
that produces those declarations, so all three call it directly.

## Measured

`checker.fern` built for x86-64-linux by the stage-2 compiler under
callgrind, main at ba445c24 against this branch. The two compilers build
`checker.fern` for x86-64, arm64 and wasm, and `fern.fern`, to byte-identical
binaries. `scripts/selfhost-emit-hashes` matches on all 2,001 rows:

| | main | structs only |
|---|--:|--:|
| stage 2, x86-64 target, total Ir | 17.987 G | 17.945 G (−0.23%) |
| `erase_view_module`, inclusive | 122 M | 81 M |
| `substitution_of`, inclusive | 5.934 G | 5.892 G |

The two erasures left are the emit's, of the lifted module, and the driver's,
of the checked module the entry check reads.
