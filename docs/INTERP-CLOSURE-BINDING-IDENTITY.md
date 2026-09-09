# Closure binding identity in the migration oracle

Issue #8970, supporting #4451, #8920, #5531 and #5986.

The Go interpreter retained mutable environment maps. A nested closure reading
an outer `n = 7` was redirected to a later `n = 99` declaration. Writes could
also modify the wrong binding. These are different declarations, not updates
to one captured variable. The new shared binding cells preserve that distinction.

The same regression set exposed an escaping array parameter whose owning count
was dropped at scope exit despite a surviving closure. A later caller update
changed the captured array. Captured cells now keep their ownership beyond
scope exit; ordinary values and borrowed map parameters keep scoped accounting.
This follows the interpreter's existing conservative GC-container lifetime
policy, not an exact closure-destruction or cycle-collection implementation.

Only captured bindings are promoted. A checked capture-free lambda has an empty
environment; checked captures select exact names, and unchecked interpretation
snapshots the visible names. Assignments share cells, declarations never replace
a cell held by another environment, and local functions bind themselves before
capture. No new AST ownership heuristic or backend optimization is introduced.

## Validation

- Seven language regressions plus direct binding-identity invariants pass.
- Three differential programs agree with independently pinned interpreter
  results on x86-64, ARM64 and Wasm: nine target/program combinations, no skips,
  0.988 seconds in the Linux ARM64 development container. QEMU is correctness
  evidence only.
- Full interpreter and COW-verifier suites pass. The coverage inventory had
  stale FloatLit and TryOp skips, now replaced with executable cases. Synthetic
  EnumLit, CaptureRef and MakeClosure inventory entries remain explicitly
  excluded from this pre-closure-conversion interpreter's source-node check.
- Full lint passes. Full integration CI remains required before merge.

## Native measurements

Apple M3 Pro, macOS ARM64, Go 1.26.0. Compared identical benchmark sources on
parent ab495b2b9 and this repair. Refreshed parent bf8911b6d has an identical
source tree. A 100 ms pilot preceded three 500 ms samples per case. These are
small oracle microbenchmarks, not compiled Fern performance measurements.

| Workload | Parent ns/op samples | Repair ns/op samples | Parent bytes / allocations | Repair bytes / allocations |
| --- | --- | --- | --- | --- |
| Ordinary locals | 309.4, 305.0, 338.1 | 321.1, 307.6, 299.6 | 416 / 5 | 384 / 4 |
| Capture-free lambda | 418.1, 460.1, 421.3 | 391.9, 423.9, 511.1 | 1008 / 11 | 920 / 9 |
| Capturing lambda | 532.3, 497.1, 503.3 | 537.4, 525.3, 523.8 | 1008 / 11 | 1272 / 12 |

The first implementation allocated a cell for every local. Lazy promotion
removed that avoidable cost; reusing the existing table also removed a second
cell table. The capturing case pays one additional allocation for the shared
binding and retains an independent capture-name table. No general speedup is
claimed from these samples.

Identical `go build -trimpath -buildvcs=false` settings give Mach-O sizes
29,162,642 and 29,163,218 bytes: **+576 bytes**. The actual instruction section
grows from 10,206,996 to 10,208,500 bytes: **+1,504 bytes**, exactly matching the
sum of changed interpreter text symbols. New capture/captureCell code accounts
for 1,104 bytes, the binding method 80, verifier changes 192, and net changes
to environment operations and their inlined callers account for the remaining
128. Other code-generation packages' instruction symbols are unchanged.

Function/line metadata grows 1,689 bytes; constant type metadata and links grow
1,140 bytes; compressed debug sections grow 2,618 bytes. Mach-O segment padding
absorbs those increases, with the final file length changing by the 576-byte
link-edit increase. No baseline was changed. This is justified oracle behavior
and lifetime accounting, not growth in generated Fern programs.
