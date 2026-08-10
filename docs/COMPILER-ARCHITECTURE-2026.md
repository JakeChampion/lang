# Compiler architecture 2026 — scorecard against the modern-design checklist

**Status:** assessment + recommendation. Not a plan; the plans it points at
are `TYPED-IR-REWRITE.md`, `SELFHOST-SYMBOL-INTERNING.md`, `SSA-DECISION.md`
and `NATIVE-CONVERGENCE.md`.

This doc answers a specific question: *given the list of architectural ideas
a compiler designed from scratch in 2026 would adopt, which ones does Fern
already have, which are worth buying, and which are actively wrong here?*

The short version: **Fern has more of that list than the list assumes, the
single highest-value item is already the live front (typed multi-level IR),
and the list's implicit cost model is wrong for this project in a way that
flips several of its recommendations from "buy" to "decline".**

## The constraint the checklist doesn't model

Every architectural change here is paid **twice** — once in the Go compiler
under `internal/` (30.7k lines of `ir`, 51.4k of `codegen`, 14.7k of
`checker`) and once in the Fern compiler under `examples/self_host/` (134k
lines). `NATIVE-CONVERGENCE.md` names that double maintenance as "the
dominant tax on the project."

That policy also sets the direction of travel: after roadmap goal 2, native
`internal/` accepts only bugfixes, oracle needs, and what the self-host
sources need to bootstrap (the "Go 1.4 rule"). Native stops being the
product and becomes the stage-0 bootstrap plus differential oracle.

Two consequences, and they are the load-bearing conclusions of this doc:

1. **Architectural investment in `internal/` is depreciating.** A beautiful
   query engine in Go buys IDE latency for a compiler slated to freeze. Any
   idea from the list worth adopting should be evaluated as *"do we want to
   build this in `examples/self_host/`?"*, because that's where it has to
   exist to matter in three years.
2. **"Rewrite the pipeline" items are priced wrong.** The checklist reads
   like a greenfield design. Here, a from-scratch re-layering costs two
   implementations, a byte-identical self-compile fixpoint, and a
   differential oracle across three backends plus an interpreter. That's not
   a reason to never do it — it's a reason to demand each layer pay for
   itself independently, incrementally, on the self-host side.

## Scorecard

### Already shipped

| # | Idea | Where |
|---|------|-------|
| 4 | Value-based IR | `ir.Op` is a flat value struct in a `[]Op` stream — no `Instruction*` graph. Rare fields moved to an `OpExt` side-table (160 B → 96 B/op) |
| 10 | Machine SSA | `internal/ssa` — dominators, dominance-frontier pruned phi insertion, `liveness`, `regalloc`. Built, tested (~469 tests), **deliberately not on the production path** — see below |
| 11 | Integrated register allocation | `ssa/regalloc.go` with `liveness.go` |
| 13 | LTO without a linker | Whole-program IR → machine code → executable, no `.o` round-trip. `internal/native/{x86_64,arm64}` assemble and `elf`/`macho` link **in-process**: ~25 s / 2.6 GB where GNU `as` took ~36 s / 4.7 GB plus a link, and the 470 MB `.s` never touches disk |
| 14 | Arena-based | The runtime is a bump arena over a 16 GiB `MAP_NORESERVE` mapping with a large-tier freelist; exhaustion is a distinct exit code (125), not an OOM |
| 18 | Deterministic compilation | `ir/determinism_test.go` + `determinism_corpus_test.go`. The byte-identical self-compile fixpoint (gen0 == gen1) is a *much* stronger determinism gate than most compilers carry |
| 23 | Self-contained toolchain | Parser, checker, optimizer, codegen, assembler (x86-64 + arm64), ELF + Mach-O writers, DWARF (`native/elf/dwarf.go`), Mach-O code signing (`macho/sign.go`), linker, package manager (`fern.toml`/`fern.lock`, MVS), LSP, doc generator. On `-target arm64-linux` the *self-host* compiler emits, assembles and links in-process — no gcc, no wasmtime |

Item 23 is the one worth stating plainly, since it was framed as an
aspiration: it's done. gcc/lld survive only as an automatic fallback when
the native assembler refuses, and refusing rather than emitting garbage is
the designed behaviour — three assembler bugs (missing GAS numeric local
labels, no `.text` symbol case, an i32-truncating literal pool) were found
exactly because the fallback masked them as codegen bugs.

### Partially there

**#1 Multi-level IR — the real lever, already the live front.** Today the
shape is AST → closure-conversion → monomorphization → flatten → flat
stack-machine `ir.Program` → backends. What's missing is precisely the
checklist's HIR/MIR distinction: **there is no typed level.** The checker
computes a `Type` for every expression, uses it to validate, and throws it
away, because AST nodes carry no type field. So lowering *re-derives* all of
it: `irlower.fern` carries ~28 structural re-inference predicates
(`expr_is_str`, `expr_is_f64`, `infer_expr_width`, …) and ~20 per-module
string-keyed registries to reconstruct what the checker already knew.

The cost is measurable and it is the largest single number in the codebase:
`irlower.fern` is **45,327 lines — 3.1× the next-largest file** (`parser.fern`
at 14.6k) and a third of the entire self-host compiler. Re-inference is why.

This is `TYPED-IR-REWRITE.md`, tracking #5531 (mechanism) and #5986 (finish
the migration), currently Phase A. It is the checklist's #1 idea, it has a
plan, and it is the correct place for architectural effort right now.

**#3 Persistent immutable data / IDs instead of pointers.** Half-adopted.
The IR side is value-based and ID-shaped; the AST side is not, and
`IMMUTABILITY-MIGRATION-PLAN.md` covers the rest.

**#15/#16 Interning and hash-consed types.** Designed, not built:
`SELFHOST-SYMBOL-INTERNING.md` (#4394 lever 1) is unblocked and specified
down to the `SymTab` threading. The motivation there is stronger than the
checklist's "huge speedup" — `IR-SELFCOMPILE-OOM-FINDINGS.md` identifies
`Op.kind`/`Op.str` strings in persistent op arrays as the dominant surviving
self-compile memory, and lever 1 turns them into i32 ids. This is a memory
fix that happens to also be the checklist's item. **Buy it.** Lever 2
(int op-tags for `Op.kind`) is the same argument.

Note the sequencing constraint that made this tractable: interning
identifiers required SH-021 first, because type names are identifiers, and
the type system used to decode type-name *strings* by hand. That's now
routed through structured `TypeRef`.

**#20/#21/#22 ABI layer, unified target description, legalization split.**
Partial and uneven. `Op.Width` carries a `WidthPtr` sentinel each backend
resolves to its own pointer width, and `ast.IsPointerType` drives
stride/offset/store-width selection — that's a real, working legalization
seam. But relocations are declared three separate times (`native/arm64`,
`native/x86_64`, `native/elf`), and calling-convention logic lives inside
instruction selection rather than behind an ABI boundary. The backends'
`emit_*` layers are *deliberately* parallel (CLAUDE.md's erasure section
calls this out), and the shared frontend already lives in `asmcore.fern` —
so the useful version of #21 here is narrow: unify the **relocation +
object-emission** model, not instruction selection. That's the part that
makes RISC-V or Windows x64 cheap later, and it's a contained diff.

### Declined, with reasons

**#2 Query-based architecture.** The right idea, evaluated in
`IDE-COMPILATION-RESEARCH.md` against rust-analyzer, and correctly deferred.
The honest numbers: current edit-to-diagnostic is 5–20 ms on playground-size
files, and the workspace-shaped case is *untested because no real-world Fern
workspaces exist yet*. Buying salsa-style incrementality now optimizes a
latency nobody is experiencing, in Go, in a compiler heading for freeze. The
cheap prerequisite from that doc — threading cancellation tokens through
parser + checker — stays worth doing; the query engine does not, yet.

**#5/#6 Unified graph IR and Memory SSA.** Gated behind the SSA cutover, and
that decision has an explicit tripwire list and a re-evaluation date of
**2026-09-01 — under a month out** (`SSA-DECISION.md`). None of the four
tripwires has fired. Memory SSA specifically buys alias analysis, and the
memory model here is reference counting with Perceus reuse — the aliasing
questions that matter are answered by `ir/rc_analysis.go` and borrow
inference, not by a memory-SSA lattice. Re-evaluate in September on the
existing schedule; don't front-run it.

**#7 Equality saturation.** Genuinely the most interesting item on the list,
and the wrong shape for Fern today. E-graphs pay off when you have a large
space of algebraically-equivalent rewrites and a cost model good enough to
pick among them. Fern's optimizer is peephole-grade by *decision*, the SSA
pass suite is shelved, and the README's representative example already
collapses to `const; return`. Adding equality saturation above a shelved SSA
layer is building the roof before the walls.

**#8/#9 Cost-based optimization and global instruction selection.** Both
presuppose the SSA cutover. #9 additionally presupposes a target description
rich enough to express latency and register pressure — i.e. #21 done
properly, which the previous section argues against for instruction
selection. Decline both; revisit only if the September re-eval cuts over.

**#12 Profile-guided everything.** Real value, wrong order. PGO needs a
stable optimizer to feed. Today the highest-leverage profile data is about
the *compiler's own* memory and allocation behaviour, and that's already
instrumented far better than a generic PGO harness would manage:
`__heap_bump_bytes()` (host-independent high-water mark — RSS is useless
here, the same binary measured 43 MB locally and 552 MB on CI depending on
THP settings), `__arr_push_shared_count()` / `__arr_push_shared_bytes()` for
the rc==1 append cliff, `FERN_CLIFF_REPORT=1`.

**#17 Fast generic monomorphization caching.** `internal/monomorph` exists
(1,882 lines) and the self-host has `monomorphize_module`. Caching
`(GenericFunction, TypeList) → MachineCode` is a compile-*time* optimization
for a compiler whose measured bottleneck is memory, not repeated
instantiation. No evidence it's hot. Not until it profiles.

**#19 Cranelift-style backend simplicity.** Already the operating
philosophy, arrived at independently: three simple backends over a flat IR,
with a rich shared frontend in `asmcore.fern`. Nothing to buy.

## Recommendation

In order, and each item is already specified somewhere:

1. **Finish the typed IR** (`TYPED-IR-REWRITE.md`, #5531/#5986). This *is*
   checklist item #1, it's the only item with a 45k-line file as evidence of
   its cost, and every subsequent layering idea gets cheaper once lowering
   reads types instead of re-deriving them.
2. **Land symbol interning, levers 1 and 2**
   (`SELFHOST-SYMBOL-INTERNING.md`, #4394). Checklist #15/#16, and
   independently justified as the fix for the dominant self-compile memory
   consumer.
3. **Unify the relocation + object-emission model** across
   `native/{arm64,x86_64,elf,macho}`. The contained, genuinely-portable
   slice of #20/#21. Leave instruction selection parallel.
4. **Let the SSA re-evaluation happen on 2026-09-01 as scheduled**, on
   tripwire evidence. #5/#6/#8/#9/#10 all resolve downstream of that one
   decision; nothing on this list is a reason to move the date.

And the meta-point, which outranks all four: **build these in
`examples/self_host/`, not `internal/`.** Under the convergence policy every
native-only addition is a debt entry against the freeze preconditions
(#4451), not a free win. The checklist's advice is sound; applying it to the
Go compiler in 2026 would be spending the architecture budget on the
implementation that's scheduled to stop being the product.
