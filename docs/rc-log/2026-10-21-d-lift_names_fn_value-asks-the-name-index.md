# 2026-10-21 — lift_names_fn_value asks the name index

`lift.lift_names_fn_value`, over `lift.MFuncs`. Refs #8171.

## The shape

The lambda lift asks, for an identifier, whether it names a receiver-less
function that is not a `const`, and did so by scanning every declaration
in the table until the first with that name. Nine call sites ask it per
identifier they consider, so on the whole compiler's self-compile the scan
was 82 M instructions, twenty times the `checker.fern` figure for a subject
five times smaller.

## What changed

`MFuncs` already indexes its declarations by name (`head`, `next`). The
answer is a walk of the name's chain keeping the lowest index, since a
chain `mfuncs_extend` grew is not ascending and the scan answered with the
first declaration in order.

## Measured

Under callgrind, private certifier memo warmed by one compile of the
subject, each compiler built by itself from its own tree, compiling one
frozen copy of main at 2210347:

| subject | base (the whole_values slice) | this |
|---|--:|--:|
| `compiler/fern.fern`, the whole compiler | 76.457 G | 76.369 G (−0.11%) |

The whole-compiler binary is byte-identical to the base compiler's.

## Measured and not landed: the gate's locals by name

`asmcore.infer_expr_type` was 230 M on the same compile, ten times the
`checker.fern` figure, and its `local_type_of` scans a function's local
names from the newest back. A `util.NameIndex` over the locals, grown with
`added` on each `bind_local_typed` and reset with the names, measured
76.369 G → 76.397 G (+0.04%): most functions bind few locals, and growing
the index per binding costs more than the short scans it replaces. Dropped;
the lead is real only for the long functions, and wants an index built once
per function rather than per binding.
