# 2026-10-03 — only a generic's instances are annotated again

`checker.ensure_annotated`. Refs #8171. No emitted byte changes: the
stage0-built compiler before and after emits the fixed older tree
(`compiler/fern.fern` at 1ae9cad, its bindings spelled `let`) and
that tree's `checker.fern` byte for byte.

## What the profile named

`checker.annotate_module` ran three times in the stage-2 compile of
`checker.fern`: once in the driver (330 M), then from `ensure_annotated` in
the lambda lift (282 M) and in the emit (288 M). Between the first and the
others, `parser.module_with_builtins_typed` instantiates the generics, and
instantiation retypes a lambda's captures by dropping them
(`captures_known: false`). Thirteen instances of `astwalk`'s generic folds
carried such a lambda, and each was enough to send the whole module back
through annotation.

## What changed

- **`ensure_annotated` annotates only what needs it.** It finds the functions,
  and the top level, holding a lambda with untyped captures or a display
  argument the rewrite has not judged, and annotates those. Everything else
  comes back as it was. `ensure_annotated_parts` does the same per part.
- **The annotation passes take a `Focus`.** `annotate_module_in`,
  `settle_literal_locals_in` and `pretype_module_in` rewrite only the focused
  functions. The module-wide inputs they read, the generic names and the
  colliding variant names, still come from the whole module.
  `annotate_module` and the other callers focus on everything.
- `has_unchecked_captures` and `has_unrewritten_display` are replaced by
  `unannotated`, one walk per function.

## Measured

The compiler each tree builds from itself through the pinned stage0
(stage 2), emitting the fixed tree's `checker.fern` under callgrind:

| | after `2026-10-03-m` | this change |
|---|--:|--:|
| total Ir | 17.774 G | 17.296 G (−2.69%) |

## Witnessed

Both emit identities, and stage 2 == stage 3. The 195 annotation, capture,
closure, lambda, display, generic and per-module tests of
`internal/e2eselfhost` pass.
