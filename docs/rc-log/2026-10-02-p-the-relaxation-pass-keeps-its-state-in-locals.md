# 2026-10-02 — the relaxation pass keeps its state in locals

`x86_native.x86_relax_pass`, `x86_relax_seen`. Refs #8171.

`x86_relax_settle` runs four passes over the branch and pad events of a
text, about 400,000 events a pass on the stage-2 compile of `checker.fern`.
`x86_relax_pass` rebuilt its `RelaxPass` state with a struct spread three
times per event, each copying the handles of all five arrays. It now works
on local arrays and builds the state once at the end of the pass, and
`x86_relax_seen` takes the arrays it reads rather than the state.

## Measured

`checker.fern` to a binary under callgrind, 4-core x86-64 container, stage 2
built from main at db70dd0.

| | main | this change |
|---|--:|--:|
| `x86_relax_pass` self Ir | 109.8 M | 51.2 M |
| `x86_relax_seen` self Ir | 29.1 M | 32.2 M |

About 55 M off a 31.82 G compile (0.17%). The run that measured it also
carried a change to `ssaunits.carried_params` that is not in this commit, so
the total is not quoted; the two functions above are untouched by it.

Byte-identical against a compiler built from main: the `checker.fern` binary,
the stage-2 compiler's output, and all 1,965 `selfhost-emit-hashes` rows.

## Not taken

`carried_params` allocates a flag per value of the function for each of its
11,000 calls. A visited list in place of the flags cut `ssaunits.bits` from
115 M to 31 M but added 70 M of `index_of_i32` scans: the walks are longer
than they look. Sharing one cleared flag array across a function's calls is
the shape that would keep the gain.
