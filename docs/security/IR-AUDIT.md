# Typed IR and verifier audit

Status: [reference] audit record for [SEC-010](https://github.com/JakeChampion/lang/issues/13512), [SEC-011](https://github.com/JakeChampion/lang/issues/13513), and [SEC-012](https://github.com/JakeChampion/lang/issues/13514).
Parent: [security epic](https://github.com/JakeChampion/lang/issues/13502).

The record reads the compiler as it stands and names each check, each gap, and the evidence behind each claim.
It changes two things, which the last section records.

## The representations

The compiler carries a function body through three representations.

| Layer | Source | What a value carries |
|---|---|---|
| Semantic graph | `compiler/semsource.fern`, `compiler/ssasem.fern` | An SSA id with an exact `typeinfo.Type`, a parameter mode (`own` is counted, any other reference is borrowed, a scalar is a value), and an anchor to the arguments its result lives on |
| Ownership plan | `compiler/ssaunits.fern`, `compiler/ssarc.fern` | The retains, releases, moves, and reuse tokens the plan inserts, with each unit's owner on every path |
| Op stream | `compiler/ir.fern` | A kind tag, an immediate, a width, a signedness bit, and a string; no type, no effect, no provenance |

The op stream is what every backend reads.
Its `Op` record has no type field, so an operand's kind and width are a property of the producing op and not of the value.
Raw memory access is four kinds, `raw_load8`, `raw_store8`, `raw_load_ptr`, and `raw_store_ptr`, each with a width.
A call is `call_direct` with a callee name and an argument count, `call_indirect`, or `call_closure_direct`.
There is no atomic op and no fence, because the runtime has one context per heap (`docs/RC-DESIGN.md`, section 2.1).
A trap is a call to a runtime helper, such as the bounds abort, and not an op of its own.

The semantic graph is where ownership and effects are explicit.
`ssasem.analyze` re-verifies every produced graph before it leaves the producer: the schema, then each operation's operand types and result type, through one `*_error` check per operation family (`parameter_error`, `copy_error`, `array_projection_error`, `record_construction_error`, `variant_test_error`, `dyn_narrowing_error`, `closure_error`, `call_value_error`, and the rest).
A producer mistake is an explicit refusal of the whole function, never a partial graph.

## The verifiers

Each verifier runs on the op stream of every function before a backend emits it, from `irverifygate.verify_or_refuse`.
A problem exits 4 and names the function, the op index, and the rule.

| Verifier | Checks | Negative tests |
|---|---|---|
| `irverify.verify_fn` | A local index inside the frame; balanced `block`, `loop`, `if`, `else`, and `end`; an `else` only on an `if`, once; a branch depth with a target; a `call_direct` with a callee name, a non-negative count, and a count equal to the callee's parameter count; a non-negative count on `call_indirect` and `call_closure_direct` | `main` and `gate_checks` in `compiler/drivers/irverify_run.fern` |
| `irverifystack.verify_stack_fn` | Every op finds its operands on the stack; a void scope leaves the stack where it found it; the then-arm and else-arm agree; the function does not fall off its end holding slots; unreachable code after a branch is not counted | `stack_checks` in the same driver |
| `irverifyrc.verify_rc_fn` | A reuse site's uniqueness gate, allocation token, and decline release name one donor; no donor box is claimed by two sites reachable from each other without a rebind | `rc_checks` in the same driver |
| `ssadeps.verify`, `ssasem` schema check, `ssaunits.verify_planned` | The SSA graph is well formed; the record and enum schemas agree; the plan's tokens are held on every path into a block or on none | `TestSelfHostSSAPhysicalRCRejects`, `TestSelfHostSSADependencyVerification*` |
| `rccertify.certify` | The emitted retains and releases balance per path, read back from the ops with no knowledge of the plan; exit 5 on a finding | `TestSelfHostRcCertify*`, the physical-RC driver |

The stack verifier skips a function that holds an op outside its arity table, and the compile-path gate counts the skip.
The corpus sweep in `TestSelfHostIRVerifyProvidedCorpusClean` holds that count at zero for the typed lowering's bodies.
On the compiler's own 9905 functions the count is zero.

## What the verifiers do not check

| Gap | Why | Consequence |
|---|---|---|
| Operand types and widths | The op stream carries no type | A well-formed stream can read a value at the wrong width; the semantic graph is the typing gate, and the translation-validation oracle accepts the width the after side reads (`docs/TRANSLATION-VALIDATION.md`) |
| A `call_direct` to a callee outside the unit | The index holds the unit's own declarations | A runtime helper or another unit's function is not arity-checked at this gate; `asm_modload_run -verifyprovided` resolves callees over the whole import closure |
| A `call_indirect` signature | The op carries a count and no type | Native targets dispatch through a function pointer with no check; Wasm checks the type at the call and traps. The checker types every function value, so a mismatch needs a compiler bug |
| Alignment of a raw load or store | The width is on the op and the address is a value | A misaligned raw access is the caller's obligation under `docs/security/UNSAFE-INVENTORY.md` |
| Effects and provenance | Not represented | No pass can prove an access is in bounds from the IR alone; bounds stay a runtime check (`docs/security/BOUNDS-AUDIT.md`) |
| Atomics | No shared heap | SEC-009 decides the model first |
| Dominance | Control flow is structured | Scope nesting and branch depth are the whole CFG rule |

## Differential and translation validation

`internal/testing/tv` proves each rewrite of the strength-reduction pass with Z3, over every input state, between barriers.
Floats, pointer-width ops, memory, and calls are barriers, so the oracle covers integer arithmetic and not memory.
`TestDifferential_TranslationValidationStrength` runs it over the fernsmith programs of a seed window.

`internal/testing/fernsmith` generates programs from a seed and shrinks a failing seed.
`TestDifferential_SelfHostX86_64` compares the self-host compiler with the Go interpreter on exit codes and output.
Half of the seeds run with a random pass configuration from `selfHostPassSwitches`: the reuse pass, inlining, array fusion, and the scale and product kernels can each be left out.
That is the optimized-against-unoptimized comparison SEC-012 asks for, on exit codes and output, which includes a bounds abort's exit 134 and the checked-arithmetic results.
The lanes do not compare the trap's message or the faulting address.

## Changes in this record

The IR verifier and the three pass re-checks it gates were off unless `FERN_IR_VERIFY` was set.
The test harness set `quiet` for every self-host compile, so a plain user compile ran without them.
The compiler's own self-compile measures their cost: 9905 functions, 90.9 s with every check and 93.3 s without, with byte-identical output.
They are now on unless `FERN_IR_VERIFY` is set to an off value.
`FERN_IR_VERIFY=1` adds the coverage line, `FERN_IR_VERIFY=quiet` is the default's spelling, and `FERN_IR_VERIFY=0` leaves every check out.

The arity rule accepted a `call_direct` with one argument fewer than the callee declares.
No commit message records why.
The compiler's self-compile and the self-host fixture corpus pass with the allowance removed, so the rule now requires the exact count.
The driver case that asserted the allowance now asserts the refusal.

## Commands

```
FERN_IR_VERIFY=1 bin/fern -target x86-64-linux -embed build/embed -o OUT compiler/fern_main.fern internal/stdlib
bin/fern -target x86-64-linux -embed build/embed -o OUT compiler/fern_main.fern internal/stdlib
go test ./internal/testing/e2ecompiler -run 'TestSelfHostIRVerify'
FERN_SELFHOST_FIXTURES=1 go test ./internal/testing/e2e -run 'TestFernFixturesSelfHost(X86_64|Wasm|Arm64)$'
```

Host: Linux 6.18.44-fc-v165, Intel(R) Xeon(R) Processor @ 2.80GHz, Go go1.27.3, QEMU 8.2.2, wasmtime 46.0.1.
