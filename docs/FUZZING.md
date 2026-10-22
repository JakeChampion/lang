# Fuzzing Fern

Status: [reference] the fuzzing system as built, with its roadmap.

How the fuzzing system is built, what each part can and cannot catch, how it
is measured, and what comes next. The goal is the strongest fuzzing system any
language implementation has, judged by a number rather than by assertion: the
**mutation score** below.

`docs/TEST-GATES.md` remains the authority on which lane gates what; this file
is the design and the roadmap.

## The parts

### Generators

**fernsmith** (`internal/testing/fernsmith`) writes whole programs that parse
and type-check by construction, wasm-smith style: every production picks
operands whose types fit the context. Every decision goes through a `chooser`,
so the same generator is driven two ways:

- `Gen*(seed)` — a PCG stream; the seeded sweeps.
- `Gen*Bytes(data)` — one decision per input byte, for `go test -fuzz` and the
  coverage-guided loop. An exhausted stream takes the smallest branch of every
  decision, so cutting bytes off a corpus never grows the program
  (`TestGenBytesShrinkIsMonotonicAndValid`), and `ShrinkMainBytes` reduces a
  failing corpus to a small repro (Hypothesis-style choice-sequence reduction,
  MacIver and Donaldson, ECOOP 2020).

Three profiles: free-form (parse/check only), runnable (`main` returns a byte),
and printable (`main` prints observations, which admits floats through
portable channels).

What a program can contain:

- **Types**: every scalar width signed and unsigned, strings, arrays, tuples,
  maps, Option/Result, closures and closure arrays, float aggregates, and
  per-program structs and enums whose fields and payloads may hold boxed values
  and earlier-declared nominals — so a struct can own an array of closures and
  an enum can carry a struct. Each declared nominal can get an array type of
  its own (`S0[]`).
- **Reads through nested values**: `accessPaths` projects up to three steps
  from any in-scope var (`s.f0.fst`, `arr[0i32].f1`, `t.0`).
- **Ownership transitions** (`ownership.go`): build a nested value, alias it,
  functionally update it while shared (`S { ...v, f: e }`), project a boxed
  field out, and reassign the original while the alias still holds it — the
  shapes Perceus reuse and nested retain/release have to get right.
- **Closure environments** (the closure cluster in `ownership.go`): a lambda
  whose parameter and first capture share a type, combined so that swapping
  them changes the result, called directly or through an array — the
  environment/argument mix-up the #5001 / #5007 / #5009 / #5026 cluster was
  made of. With it the planted closure swap is caught at corpus 18; at random
  it took 418.
- **Control**: bounded `while`, `for` over arrays and maps, labelled
  break/continue, defer, match with guards and `@` bindings, if-let, let-else,
  the `?` operator, local functions and lambdas.
- **Recursion**: a helper may take a hidden `__fuel` bound, return early when
  it runs out, and call itself with one less at most twice outside loops and
  closures, so a call costs at most 2^(fuel+1) invocations.
- **Generics and methods**: the prelude's `id[T]` / `pick[T]` and Pair methods.
- **Traits and `dyn`** (`traits.go`): a user trait with a required method and
  a default method that calls it, implemented by up to three of the program's
  structs and `i32`, each impl's body a generated expression and some impls
  overriding the default. A bounded generic (`tg0[T: Tr0]`) dispatches
  statically and is monomorphised per implementer; `dg0(d: dyn Tr0)` and
  `da0(ds: dyn Tr0[])` dispatch through the vtable. Concrete values reach
  them by coercion at the call site, and a cluster in a body builds a `dyn`
  array of mixed implementers and reassigns a `dyn` local from one
  implementer to another, dropping the first object. The cluster also puts a
  trait object in a struct field (`DH0 { d: dyn Tr0, k: i32 }`, then a
  functional update that shares it) and in a tuple, and downcasts the
  reassigned local or an array element with `as?` to a struct implementer.
  Two optional traits sit beside it: `Mk0`, whose constructor is an
  associated function the generic `tm0[T: Mk0]` calls as `T.mk0_new(x)`, and
  the generic trait `Tc0[T]`, implemented at `i32` and reached through both
  `tcg0[T: Tc0[i32]]` and `dyn Tc0[i32]`. A third, `Pr0`, declares an
  associated type each implementer binds to `i32`, `i64` or `string`;
  `tp0[H: Pr0](h: H): H::Item` returns the projection at whatever that is,
  and `dyn Pr0[Item = i32]` dispatches to the `i32` ones.
- **Consuming receivers** (`ownrecv.go`): `function (own b: OB0) push(x)`,
  which appends to the struct's array. A cluster reassigns a local through it
  (`b = b.push(..)`, sometimes in a loop), ends with a last-use call, and
  sometimes aliases the array first, so an append that grew a shared array in
  place changes what the alias reads.
- **Generic structs and enums** (`generics.go`): `G0[T]` and `GO0[T]` with
  methods, a functional update of the generic receiver inside a method, and a
  generic constructor, instantiated at a type drawn from main's pool, boxed
  ones and function types included. The variant is built from a method's
  return, so a dead receiver's box is reused for it.

**Swarm testing** (`swarm.go`; Groce et al., ISSTA 2012) draws a per-program
mask over productions and types from the first bytes of the stream. A program
that may use thirty types rarely holds two values of one type, so the
productions that need a pair hardly fire; narrowing the pool per program is
what lets them meet. Features and types are each kept with probability 3/4:
several features only fire together, and at 1/2 their conjunction is too
rare to matter.

**State fold**: `main` folds every i32 and boolean reachable from an in-scope
var into the value it returns (printed, in the printable profile), the way
Csmith checksums global state. Without it a wrong value anywhere the return
expression did not read passes unseen.

The other generators are narrower: `genNumProgram` (numeric properties),
`genSimProgram` (deterministic-simulation properties), and `std/fuzz` inside
the language.

### Oracles

| Oracle | Catches | Where |
| --- | --- | --- |
| Parse + check | generator bugs, checker crashes | `FuzzGenerate_ParseRoundTrips`, `FuzzParse`, `FuzzCheck` |
| Interpreter vs compiled exit byte | miscompiles reaching the return value; crashes (signal), hangs (20 s), arena exhaustion (125) | `TestDifferential_SelfHost{X86_64,Arm64,Wasm}` |
| Interpreter vs compiled stdout | miscompiles of printed values, floats via portable channels | `TestDifferential_PrintableStdout`, `FuzzGenerate_StdoutAgrees` |
| Pass configuration | an optimisation whose off switch changes the answer, or a bug only the unoptimised path has | the non-sanitize `TestDifferential_SelfHost{X86_64,Arm64,Wasm}` legs compile half the seeds with a seed-chosen subset of `selfHostPassSwitches` (reuse, unit-plan replay, inlining, array fusion, the ndarray kernels) off |
| **Sanitizer** | rc over-release, use of a freed block, leaks — wrong memory with a right answer | `TestDifferential_SelfHostSanitize{X86_64,Arm64,Wasm}`; wasm carries the leak census only |
| Go checker vs self-host checker | a program one checker accepts and the other rejects | the self-host legs: a seed the self-host refuses to compile fails or skips by cause |
| Compiler self-checks | a lowering the compiler's own verifiers reject: the typed lowering refusing part of a program, the rc certifier showing unbalanced counts, the IR verifier finding malformed IR | every self-host leg and the coverage loop fail such a seed (`compilerRefused`) rather than skip it as a coverage gap |
| Checker mutants | a soundness hole or a completeness gap in either checker, on programs fernsmith never writes: each with one `let` annotation erased, or one `let`, parameter, result or struct-field type replaced (`fernsmith.TypeMutants`); both checkers must agree, and an erased program that checks must compute the original's answer | `TestDifferential_CheckerMutants` |
| Coverage feedback | steers mutation toward new self-host compiler paths, and every input that reaches one is judged by the exit-byte differential | `TestSelfHostCoverageGuidedFuzz` |

### Bisecting a failure to one decision

Each optimising pass asks `compiler/optbisect.fern` before it rewrites a
function: inlining a callee, fusing a pipeline, a byte-copy loop, an ndarray
kernel, reuse in one function's rc lowering. The decision's key is the pass
and the function's name. `FERN_OPT_TRACE=1` lists every decision with its key's
hash, and `FERN_OPT_BISECT=h` declines each one hashing below `h`. Declining
a decision leaves the function as it was, so the answer must not change.
When a differential leg fails a seed, or the typed lowering refuses one, it
bisects `h` over the traced hashes (`e2eharness.BisectOptDecisions`) and logs
the decision the failure follows: `inline da0`, not a whole program and a set
of switches. A test process bisects at most 16 seeds, so a fault that fails
most of a window costs a shard minutes, not its budget.

Each failed seed is also a signature: the leg, the failure's class (its
message without the seed's numbers, or the typed lowering's refusal reason
without the function), and the bisected pass. With `FERN_FINDINGS` set, the
legs append one JSON line per failed seed, and the nightly report posts
them grouped by signature (`scripts/nightly-findings.mjs`), with a count and
the first seeds of each. One fault failing two hundred seeds is one row, and
the same row on consecutive nights is the same bug.

### Measuring it: the mutation score

`TestFernsmithMutationScore` (`FERN_MUTATION_SCORE=N`) plants one fault at a
time in the lowering, rc and x86-64 emission sources — an operator swap or a
deleted assignment — builds the faulty compiler, and checks whether any of
`FERN_MUTATION_SEEDS` generated programs, built under `-sanitize`, tells it
apart from the interpreter. A mutant compiler that runs past two minutes on
one program is killed as a hang. A fault the compiler cannot even build is
"stillborn" and not scored; one that breaks a trivial program is reported apart.
The score is the share of the rest that fernsmith kills, and the list of
survivors is the generator's to-do list: each is a fault no generated program
reaches or no oracle sees (or an equivalent mutant, which nothing can kill).

This is the number a "best" claim has to rest on (Görz et al., "Systematic
Assessment of Fuzzers using Mutation Analysis", USENIX Security 2023). Bugs
found depend on which bugs exist; the score does not. `scripts/fernsmith-mutant`
is the earlier, single hand-written plant.

## What it has found

The first sweeps of the reworked generator (October 2026), each fixed with a
regression test where the bug lived:

- **E049 read value blocks as closures** (self-host checker). An `if` or
  `match` expression lowers to a value block, a zero-argument lambda, and the
  capture rule took its reads for captures and its assignments for
  write-backs, rejecting valid programs. Rows in `TestSelfHostCheckerCodesX86_64`.
- **E049 read generic declarations unsubstituted** (Go checker). `Option[i32]`
  counted as able to reach a function because `Some`'s payload still names the
  parameter `T`. `TestOuterRebindOfFunctionFreeCaptureAllowed`.
- **An unlinkable binary from the tree shake.** An enum with a map-holding
  variant, named only in the signature of a nested function nothing calls,
  kept its drop glue but lost the map runtime it calls.
  `TestSelfHostTreeshakeKeepsMapDropForSignatureEnumX86_64`.
- **A lambda arm left unboxed by the lift.** In a value block whose other arm
  reads a function out of a struct field or array, the lift gave up boxing
  the whole block and the typed lowering refused the bare function address.
  `TestSelfHostIIFEFnArmIR*`.
- **A register-allocator miscompile.** A parameter's live interval started
  at its first read rather than at entry, so two parameters could share a
  home register and the entry's moves overwrote one with the other: a
  segfault on x86-64 and arm64. Found when the planted-mutant check's shrunk
  repro crashed the unplanted compiler too. `TestSelfHostSSAParamInterval*`.
- **Generics inside `defer` refused** (#12276). The monomorphisers' statement
  walkers had no `defer` arm, so a bounded generic call or a generic struct
  literal in one kept its unmangled name. `TestSelfHostMonoDefer`.
- **A reused box tagged with the bare variant name** (#12288), a wrong answer
  and a leak. A generic enum's variant built in a dead box took the
  unqualified shape word, so a method's match took the other arm and the
  payload was never released. `TestSelfHostGenericReuseTag`.
- **A function type in a symbol** (#12289). `G0[(i32) => i32]` put `=>` into
  instance labels GNU as rejects. `TestSelfHostFnTypeArgMangle`.
- **A dyn method's result read without the dyn's arguments** (#12629,
  self-host checker). `T` in `dyn Tc[i32]` and `Self::Item` in
  `dyn P[Item = i32]` stayed unresolved, so -check refused valid programs.
  `TestSelfHostDynTraitArgsCheck`.
- **A dead arm never type-checked** (#12631, Go front end). The constant fold
  pruned an `if (false)` arm before the checker ran, so a type error behind it
  passed. `TestFoldKeepsDeadArmsForTheChecker`.
- **`let v = None;` broke the re-check** (#12636, Go checker). The stamped
  bare `Option` failed its own arity when monomorphisation re-checked a
  function that also made a generic call. `TestUninstantiatedEnumLocalSurvivesRecheck`.
- **The checkers disagreed on an `Err` payload nothing typed** (#12638).
  `let r = Ok(5);` leaves `E` unbound. The fix first made the payload refuse
  to sit in a typed array, tuple, variant or map. #12788 replaced that rule:
  the payload is never held, so its arm is unreachable and it fits anywhere.
  `TestSelfHostUnboundPayloadCheck`.
- **No E040 for an uninferred receiver** (#12632, self-host checker).
  `let o = GNil; o.count()` binds no `T`. Rows in
  `TestSelfHostCheckerCodesX86_64`.
- **A dyn call's arguments went unchecked** (#12639, self-host checker; from a
  review of #12627). `TestSelfHostDynTraitArgsCheck`.
- **An associated call's arguments went unchecked** (#12693, self-host
  checker). `T.mk(true)` through a bound, and `S.mk(true)` through a trait
  or a derive in a local function. Rows in
  `TestSelfHostCheckerCodesX86_64`.
- **An impl's signature was compared with its trait's only partly** (#12694,
  self-host checker). An associated function, a `self` not typed `Self`, an
  overridden or inherited default, and a generic trait's requirement were
  all skipped. Rows in `TestSelfHostCheckerCodesX86_64`.
- **A struct-update base of another type had no diagnostic** (#12695,
  self-host checker), so a local function accepted it. Rows in
  `TestSelfHostCheckerCodesX86_64`.
- **A generic literal passed to a generic parameter took the callee's `T`**
  (#12739, Go checker), so `b(G { v: 1 })` was refused.
  `TestGenericStructLitFieldCheckedAgainstDestination`.
- **A type parameter was a wildcard in its own generic's body** (#12656,
  self-host checker). `return 0` into a `T`, an `if` joining a `T` with an
  `i32`, a field of type `T` given an `i32`, and `H::Item` returned as a
  `string` were all accepted. A body's type parameters are now
  `TypeErased`, equal only to themselves (`rigid_tparams`); a caller's
  reading of the signature still erases them to unknown for inference. Rows
  `rigid-tparam-*` in `TestSelfHostCheckerCodesX86_64`.
- **An array literal fitted any `T[]` in a generic body** (Go checker; found
  with #12656). `elemSettleable` let a destination element naming the
  enclosing declaration's own parameter take any literal. Only a callee's
  parameter does now. `TestArrayLiteralSettlingStillWorks`.
- **A missing method on a `boolean` had no diagnostic** (self-host checker;
  found with #12656), so a generic body, which gets no well-typedness hint,
  accepted it. Row `bool-method-missing-in-generic`.
- **A function or tuple type could not be a call's type argument** (#12775,
  both parsers). `id[(i32) => i32](g)` and `id[(i32, string)](t)` were read
  as an index. A `(` opening a type keyword, or a `=>` at the bracket's own
  depth, now marks the bracket as type arguments. `TestCallTypeArgsShapes`,
  `TestSelfHostTypeArgsGrammar`, `TestSelfHostFnTypeArgMangle`.
- **`as?` on a struct or enum had no E059** (#12787, self-host checker),
  which flagged only a scalar operand. Row `as-downcast-struct-left`.
- **An enum argument no construction settled** (#12788, both checkers and
  the lowering). `let o = None;` and `let r = Ok(1);` leave a payload the
  binding never holds. Go typed a pattern binding of it as the raw `T` or
  `_`. The self-host checker refused it uncoded, and the lowering refused a
  `Some(x)` arm on a bare None. Both checkers accepted storing a settled
  value into the binding, which the interpreter then mis-ran. Now the binding
  is unreachable (never), a `?` on it has nothing to propagate, and the store
  is E003. Rows `unsettled-*`, `TestPartialEnumPayloadInference`,
  `TestSelfHostBareNoneBinding`.
- **A bounded generic called with an `as?` payload had no instance**
  (#12797, self-host monomorphiser), which typed `x as? S` as `x`'s own type,
  so `match (d as? S) { Some(s) => tg(s, …) }` left `tg` uninstantiated.
  `TestSelfHostDynTraitIR` row `downcast-into-bounded-generic`.
- **A `for` loop inside a `defer` went unchecked** (#12789, Go parser): the
  foreach desugar did not enter a `defer`, and the checker has no case for
  a standing ForEach. `TestForEachInDeferLowers`.
- **A generator bug**: the ownership cluster reassigned a variable whose type
  can reach a function while a later closure captured it (E049 by the
  language's own rule). The generator now asks `reachesFn` first.

Earlier: the closure-dispatch cluster (#5001, #5007, #5009, #5026), the
`i64[]` narrowing (#5729), the u32 shift that miscompiled SHA-256, the float
multi-slot payload (#7333), and the self-host wasm traps (#9995) — the last of
which the wasm differential now gates, with wasmtime installed on its lanes.

## Research

What the field does, ranked by expected yield for Fern given what is already
built. Citations are to the paper that introduced the technique.

1. **Memory-management oracles on every generated program.** No published
   compiler fuzzer checks refcount correctness; Fern has the sanitizer, the
   rc certifier and the leak census already. Done on all three targets (the
   sanitizer legs), and reuse off is one of the pass configurations below.
   The rc certifier runs on every compile, and its finding fails the seed.
   Next is its report mode's census of values it could not prove, which
   no leg reads yet.
2. **State hashing** (Csmith, PLDI 2011; rustlantis, OOPSLA 2024; DUMPLING,
   NDSS 2025). Done as the state fold.
3. **Swarm configuration and generation policies** (Groce et al., ISSTA 2012:
   42% more distinct crash bugs than Csmith's tuned default; YARPGen, OOPSLA
   2020 and PLDI 2023: biased value distributions and loop shapes). Swarm done;
   policies partly (boundary-heavy integer literals).
4. **Pass-configuration differential with a fuel-bisectable chaos mode**
   (Cranelift's `cranelift-control`, LLVM's opt-bisect-limit). The compiler has
   switches (`FERN_SELFHOST_NO_REUSE`, `FERN_SEM_INLINE`,
   `FERN_NO_ARRAY_FUSION`, ...). Done: the differential legs flip them per
   generated program, and a failure bisects to the one (pass, function)
   decision that caused it. The key is a hash rather than a running count, so
   the compiler needs no mutable state, and the key of a decision does not
   depend on which other decisions were declined.
5. **Component fuzzers with symbolic checkers** (regalloc2's checker,
   cranelift-fuzzgen, CLIR): generate SSA directly and check the register
   allocator and the rc plan replay in isolation.
6. **Coverage-guided generation** (Zest/JQF, ISSTA 2019 — fernsmith's byte
   stream already makes it a parametric generator). Done: the self-host
   coverage loop judges every input that reaches new coverage with the
   exit-byte differential.
7. **EMI and semantics-preserving transformations** (Orion, PLDI 2014; Hermes,
   OOPSLA 2016; GraphicsFuzz, OOPSLA 2017): mutate real programs — the
   conformance corpus, coreutils, the compiler itself — in ways that must not
   change their output.
8. **Type-checker fuzzing** (Hephaestus, PLDI 2022; Thalia, POPL 2024): erase
   annotations and overwrite types, and hold both checkers to one verdict.
   Done as the checker-mutant oracle, in the x86_64 `diff-selfhost` lane. It
   erases `let` annotations and a generic call's type arguments, and
   overwrites `let`, parameter, result and struct-field types.
9. **Fragments mined from bug history** (LegoFuzz, OOPSLA 2025; FeatureFuzz,
   2026; WhiteFox, OOPSLA 2024): `docs/rc-log/` and the tracker are a corpus of
   shapes that broke once.
10. **Small-scope enumeration** (Skeletal Program Enumeration, PLDI 2017).
11. **Triage**: one signature per finding (pass + bail site, or sanitizer
    report + function) instead of one issue per night, ranked by
    Furthest-Point-First (Chen et al., PLDI 2013), with each reduced repro
    promoted to a regression test. Done: the signature (leg, failure class,
    bisected pass) and the grouped nightly table. Next is the ranking and
    the promotion of each reduced repro.

## Roadmap

In order:

1. Mutation-score floor in CI once the score is measured over enough mutants
   to be stable, and the survivors worked down.
2. Rank the nightly signatures (Furthest-Point-First) and promote each
   reduced repro to a regression test.
