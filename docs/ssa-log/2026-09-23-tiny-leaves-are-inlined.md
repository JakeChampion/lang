# Tiny call-free functions are spliced into their callers

`ircore.inline_tiny_leaves` is native `internal/ir/inline.go`'s tiny-leaf
mode for the self-host's native targets, run on the lowering the register
backends emit (after `sub_cache`, so the typed path's bodies are the ones
spliced). What it took to make it pay, measured 2026-09-23 on x86-64:

| policy | `checker.fern` static | stage-2 binary | stage-2 Ir (`ssa.fern`) |
|---|---|---|---|
| none | 479,958 | 10,863,416 | 4,034 M |
| leaves may call runtime helpers | 491,773 (+2.5%) | 11,281,512 (+3.8%) | 4,038 M |
| call-free leaves, originals culled | 480,747 (+0.16%) | 11,055,952 | 4,039 M |

A retain or release is a runtime helper call in this IR, so a leaf that
holds one keeps its call after the splice and grows the site around it;
only a body that calls nothing at all is worth splicing. Its size is
counted without its local loads and stores, which the typed lowering emits
for every value and the SSA lift removes, at 16 other ops. Native's cap
(24) is over its own op count, which does not have that padding. A unit
over native's `inlineMaxUnitOps` (20,000) has its growth budgeted at a
tenth of its ops, as native does; below it nothing is budgeted.

On the compiler it is neutral, which is what native found over its ceiling
too. On programs made of small helpers it is not:

| bench | Ir change |
|---|---|
| `call_overhead` | -45.5% |
| `struct_drop` | -1.6% |
| `string_scan` | -0.1% |
| everything else measured | 0% |

A leaf whose every reference was spliced away is culled (marked
superseded, which no emit writes) when it is a free function with a source
name and not `main`, an import or an export; a method or a `__` name can be
reached without a reference in the ops, so it stays.
