# 2026-10-04 — the checker walks a body once per question

`checker`, both targets. The first pass of #11303, refs #8171.

## The shape

Two checker queries walked a function body more often than the question
needed.

- `e049_lambda_var_types` gives an unannotated variable the type `fn` when
  some `let` outside a lambda body initialises it with a lambda. It asked
  `e049_var_init_is_lambda` once per variable, and each call folded the
  whole statement list looking for that one name: a body with N unannotated
  variables was walked N times. Compiling `checker.fern` made 7,234 of those
  calls.
- `unannotated` folded every statement twice, once with `unchecked_capture`
  and once with `display_unrewritten`, to OR two boolean answers.

`e049_lambda_init_names` now lists every lambda-initialised name in one walk,
taken lazily at the first unannotated variable, and the per-variable test is
a lookup in that list. `unannotated` folds once with a callback that applies
both checks. Neither answer changes, so the emitted binary does not.

## Measured

`checker.fern` built for x86-64-linux by the stage-2 compiler under
callgrind, main at 411e0f89 against this branch at 735dd37e, both compilers
building the same `checker.fern` to a byte-identical binary. The later
review commits only remove work, so these figures understate the gain:

| | main | one walk per question |
|---|--:|--:|
| stage 2, x86-64 target, total Ir | 20.374 G | 20.309 G (−0.32%) |
| `e049_lambda_var_types`, inclusive | 46.0 M | 5.2 M |
| `unannotated`, inclusive | 60.2 M | 35.8 M |
| `astwalk.fold_expr_nodes` for `boolean`, self | 160.9 M | 118.4 M |

## What is left

The other repeated walks #11303 lists are separate queries over the same
body, not one query asked per name, so each needs its own look:
`parser.erase_view_body` (88 M), `semsource.escaping_bindings` (87 M),
`checker.slc_call_names` (50 M) beside `fnsigs.call_names_of_body` (39 M),
which may collect the same call names twice, and `parser.rta_has_index_call`
(47 M). The three `semsource` folds in
`escaping_bindings` were left apart on purpose: fusing them changes the order
of the list they return.

Two places that look repeated are not:

- `erase_view_module` runs three times per x86 build at about 40 M each. It
  runs once on the CLI's module for the entry check, once on the gated module
  for the borrow inference, and once on the lifted module for the emit. Each
  is a different module, and the borrow inference reads the bodies.
- `needs_pretype` scans every body twice, at 20 M a scan. One scan is the
  checker's and one is the annotate pass's, over a module that changed in
  between.
