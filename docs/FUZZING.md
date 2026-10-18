# Fuzzing Fern

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
| Checker mutants | a soundness hole or a completeness gap in either checker, on programs fernsmith never writes: each with one `let` annotation erased or replaced (`fernsmith.TypeMutants`); both checkers must agree, and an erased program that checks must compute the original's answer | `TestDifferential_CheckerMutants` |
| Coverage feedback | not an oracle: steers mutation toward new self-host compiler paths | `TestSelfHostCoverageGuidedFuzz` |

### Measuring it: the mutation score

`TestFernsmithMutationScore` (`FERN_MUTATION_SCORE=N`) plants one fault at a
time in the lowering, rc and x86-64 emission sources — an operator swap or a
deleted assignment — builds the faulty compiler, and checks whether any of
`FERN_MUTATION_SEEDS` generated programs, built under `-sanitize`, tells it
apart from the interpreter. A fault the compiler cannot even build is
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
- **An `Err` payload nothing typed fit any array** (#12638, self-host
  checker). `let r = Ok(5);` leaves `E` unbound, and the payload gave way to
  concrete siblings. `TestSelfHostUnboundPayloadCheck`.
- **No E040 for an uninferred receiver** (#12632, self-host checker).
  `let o = GNil; o.count()` binds no `T`. Rows in
  `TestSelfHostCheckerCodesX86_64`.
- **A dyn call's arguments went unchecked** (#12639, self-host checker; from a
  review of #12627). `TestSelfHostDynTraitArgsCheck`.
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
   sanitizer legs), and reuse off is one of the pass configurations below;
   next is running the certifier in report mode.
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
   generated program. Next is a fuel counter, so a failure bisects to the one
   decision that caused it.
5. **Component fuzzers with symbolic checkers** (regalloc2's checker,
   cranelift-fuzzgen, CLIR): generate SSA directly and check the register
   allocator and the rc plan replay in isolation.
6. **Coverage-guided generation** (Zest/JQF, ISSTA 2019 — fernsmith's byte
   stream already makes it a parametric generator). The self-host coverage loop
   exists but judges by `-check` only; putting the differential oracle in that
   loop is the step.
7. **EMI and semantics-preserving transformations** (Orion, PLDI 2014; Hermes,
   OOPSLA 2016; GraphicsFuzz, OOPSLA 2017): mutate real programs — the
   conformance corpus, coreutils, the compiler itself — in ways that must not
   change their output.
8. **Type-checker fuzzing** (Hephaestus, PLDI 2022; Thalia, POPL 2024): erase
   annotations and overwrite types, and hold both checkers to one verdict.
   Done as the checker-mutant oracle, in the x86_64 `diff-selfhost` lane; next is mutating more than `let` annotations (parameter and
   return types, generic arguments).
9. **Fragments mined from bug history** (LegoFuzz, OOPSLA 2025; FeatureFuzz,
   2026; WhiteFox, OOPSLA 2024): `docs/rc-log/` and the tracker are a corpus of
   shapes that broke once.
10. **Small-scope enumeration** (Skeletal Program Enumeration, PLDI 2017).
11. **Triage**: one signature per finding (pass + bail site, or sanitizer
    report + function) instead of one issue per night, ranked by
    Furthest-Point-First (Chen et al., PLDI 2013), with each reduced repro
    promoted to a regression test.

## Roadmap

In order:

1. Mutation-score floor in CI once the score is measured over enough mutants
   to be stable, and the survivors worked down.
2. Chaos fuel: a decision counter in the compiler, so a pass-configuration
   failure bisects to one decision.
3. Differential oracle inside the coverage-guided loop.
4. Signature-based nightly triage.
