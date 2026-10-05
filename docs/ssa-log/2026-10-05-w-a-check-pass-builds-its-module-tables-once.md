# 2026-10-05 — a check pass builds its module tables once

Self-host checker, every target. Refs #11534, #8171.

## The shape

A module's tables are its signature, struct, union and method tables, and
the spelling memo they resolve types through. `module_env` builds them from
a module's declarations. Each check pass built them three times over the same
module:

- `check_module_pass` built them in its head;
- `own_diags` (E050/E051) called `module_env` again;
- so did `e049_diags` (E049).

`check_module` then called `module_env` a fourth time to find the generic
instantiations to re-check.

`check_module` now builds the tables once per pass and hands them to
`check_module_pass`, which reads its tables off them and passes them to
`own_diags`, `e049_diags` and `instantiated_clones`. The re-check over the
clones checks a different module, so it builds its own. The head of
`check_module_pass` that duplicated `module_env` is gone.

Compiling `checker.fern`, table builds fall from 42 to 22.

## Measured

`checker.fern` built for x86-64-linux by the stage-2 compiler under
callgrind, main at f22d73e9 against this branch. The two compilers build
`checker.fern` for x86-64 and arm64, and `fern.fern`, to byte-identical
binaries. `scripts/selfhost-emit-hashes` matches on all 2,001 rows:

| | main | tables once |
|---|--:|--:|
| stage 2, x86-64 target, total Ir | 19.010 G | 18.880 G (−0.68%) |
| `check_module`, inclusive | 3,059 M | 2,929 M |
| `with_spellings`, inclusive | 142 M | 89 M |
| `collect_func_sigs`, inclusive | 105 M | 60 M |
| `own_diags`, inclusive | 539 M | 487 M |
| `e049_diags`, inclusive | 142 M | 90 M |

## What is left

The other 22 builds come from passes that each see a different version of
the module:

- `settle_literal_locals`, `pretype_module` and `annotate_module` run on the
  module as each earlier rewrite left it (`build_module_tables`, 9 builds);
- `prepared_module` injects the builtin structs after those rewrites;
- semsource reads the final module's scopes (`module_scopes`, 6 builds).

Sharing those needs the declarations kept apart from the bodies the rewrites
change, which is the node-id and side-table work in #11534.

A memo of each spelling's parse, beside its resolved type, was tried first and
saved 0.025%. The spellings that are expensive to parse are generic callees'
parameter types. Those callees are declared in other modules, so the memo
mostly missed.
