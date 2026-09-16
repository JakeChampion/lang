# 2026-09-16 — a labelled exit and a default are nothing new

Two declaration-level refusals that guarded nothing.

**A labelled loop.** `break outer` and `continue outer` refused the loop
and its function ("unsupported labelled loop", four corpus sites). The
loop record now carries its label and an exit names the loop it leaves:
the edge records the environment at THAT loop's entry, so the bindings
the inner loops made die on it, and the branch goes to that loop's exit
or header. Nothing inside the inner loops needs to know: their own back
edges and breaks are the ones their bodies produced.
`loop_rotation_shapes` and `defer_control_flow` produce whole and match
native under the leak check.

**A default argument.** A declaration with a defaulted parameter was
refused whole ("unsupported default argument", six sites), yet the
parser's `fill_default_args_module` has already rewritten every call site
to pass the value by the time the substitution runs, so the parameter is
an ordinary one. The refusal is gone. `default_args`,
`default_args_const` and `named_args` produce whole and match native.

## Pinned

`TestSelfHostSemanticSourceRC` runs `labelled_sum` (a labelled continue
and break out of an inner loop holding a fresh string, taken and not
taken) and `inc_calls` (a defaulted parameter passed explicitly, since the
RC harness does not run the parser's filling pass) on all four targets
under the leak check; the print golden carries `labelled` and
`with_default`.
