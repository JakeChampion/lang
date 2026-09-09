# Self-hosted callable contracts

Follow-up to #8960 and #8961, addressing #8962. This integrates the retained
arrow-signature resolver from the preserved typed-match work into the production
Fern checker. It is a prerequisite for typed captures and pre-RC semantic IR,
not a claim that AST ownership analysis has been retired.

## Correctness boundary

Previously, retained arrows resolved to callable types with unknown parameters.
Calls through tuple and array projections could silently accept missing, extra
or wrongly typed arguments that the Go checker rejected with E004 or E038.
The new regression reproduced both the lost recursive signatures and the missing
diagnostics before the fix.

The existing semantic TypeFunc now retains ordered parameter types and known
arity at these boundaries. Bare legacy `fn` remains opaque, distinct from a
known zero-argument function. Locals, parameters, fields and returned functions
consume the same declaration-sidecar resolver, including arrays of functions.
Parameter parsing now preserves array-element signatures as other declaration
sites already did. Arrow substitution keeps the enclosing type-variable context.
Nested dynamic trait types resolve through the same ladder as top-level ones.

Value calls use shared arity and argument diagnostics whether the callee comes
from a binding, projection or another call. Existing literal-range, string-borrow
and array-element rules are retained. Free functions and methods keep their
existing bound and dispatch checks. No ownership heuristics or ABI rules change.
Callable assignment variance and the checker's other partial type rules remain
separate work; this does not claim full semantic type-system parity.

## CI correction: enum payload declarations

Full CI exposed a missed producer: enum payload fields retained a callable result
but discarded its parameter list. That represented `resume(fd)` in imported
async code as a known zero-argument call. Struct and enum payload fields now use
one declaration-type parser, including positional, named and array payloads.
No argument checks are suppressed. The existing async module-loading tests now
run through the configured x86 runner and include compiler diagnostics on failure.

Ten added enum diagnostic cases compare against the Go checker. Eight failed
before the fix, while the two genuine zero-argument controls passed. All ten
now pass, together with the prior 23 cases, callable structural contracts and
the four previously failing async module-loading scenarios (20.295 s, no skips).
`make lint-all` and full source lint pass. The broader production checker,
callable, native/Wasm function-return/payload and type resolver selection passes
in 135.179 s with no skips. Full current-head CI remains mandatory before merge.

Using the identical compiler and linker described below, compared with the
published pre-correction #8964 checker, this repair reduces `.text` by 2,188
bytes and the linked ELF by 2,072 bytes (3,328,472 to 3,326,400). Symbol deltas
fully account for instructions: shared field parser +1,232, struct field parser
-1,476, enum parser -1,944. `.rodata` and `.bss` are unchanged; unwind data grows
36 bytes. The remaining ELF difference is symbol entries (+48), symbol names
(+30) and alignment (+2). No baseline changes or runtime speed claims.

## Verification

The new public-resolver driver inspects recursive callable, array, tuple, map,
nominal, unknown and dynamic types, not the lossy debug string `fn`. The diagnostic
corpus compares 23 valid and invalid programs with the Go checker without filtering
codes. Existing TypeRef and type resolver goldens remain intact.

Local Linux ARM64 image be368d4b7a7c uses Go 1.26.8 and QEMU for x86 execution.
After the construction-helper split, contract/resolver tests passed in 9.399 s
and full source lint passed in 27.184 s while other validation was running.
`make lint-all` passed. The broader checker and function-value runtime suite
passed in 417.904 s, including x86-64, ARM64 and Wasm execution. These are test
observations, not performance benchmarks. Three tests explicitly requiring a
native x86 host were skipped locally: FnValueCaptureIRArm64,
FnptrArrayFieldIRArm64 and FnptrArrayRebindIRArm64. Full current-head CI must
cover those cases before merge; a local pass does not cover them.

## Measured code-size cost

Comparable ARM64 checker builds use the same #8959 bootstrap compiler
(Go 1.26.0, Darwin ARM64) and Linux ARM64 `gcc -static -nostdlib`, against parent
#8961 6425c5241 and this change. No baseline was edited.

| Bytes | Parent | Corrected |
| --- | ---: | ---: |
| Linked ELF | 3,322,032 | 3,328,472 |
| `.text` | 3,141,016 | 3,147,232 |
| `.rodata` | 41,201 | 41,201 |
| `.eh_frame` | 40,460 | 40,524 |
| `.bss` | 4,160 | 4,160 |

An initial in-ladder signature loop grew instructions by 8,136 bytes. Isolating
callable construction into its own helper reduced that by 1,920 bytes without
changing semantics. The remaining 6,216 bytes are completely accounted for by
symbol-size differences:

| Function group | Instruction delta |
| --- | ---: |
| Value-call diagnostics/routing, less retired arity-only helper | +4,348 |
| Callable constructors and recursive resolution/substitution | +3,304 |
| Local/result/field/parameter declaration consumers | -788 |
| Array-parameter sidecar capture | +652 |
| Simplified top-level spelling entry point | -1,300 |

The remaining ELF difference is unwind data (+64), symbol entries (+96) and
symbol strings (+64). Growth supplies previously absent contract resolution and
diagnostics; the measured construction overhead was reduced before publication.
No runtime speedup, allocation improvement or self-compilation timing claim is
made from these size measurements.
