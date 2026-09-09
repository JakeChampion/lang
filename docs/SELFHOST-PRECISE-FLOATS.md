# Precise self-hosted float types

This integrates the float-type prerequisite from the preserved typed-match work
into the production Fern-written compiler. It addresses #8965 and advances the
typed-match and pre-RC ownership work in #8930 and #8920.

## Contracts

- The existing shared `TypeFloat` records concrete width and literal polymorphism.
  Concrete f32 and f64 values are not implicitly interchangeable; an uncommitted
  literal can settle to the required width. The Type union order is unchanged.
- Arithmetic, comparison, assignment, calls and returns use width-aware rules.
  Scalar and tuple result joins preserve concrete commitments independently of
  arm order. A preceding literal cannot mask later conflicting concrete widths.
- Branch terminal values are checked after their preceding local declarations.
  Pattern scopes receive the full scrutinee Type rather than only its enum name.
  Option/Result payloads retain instantiation arguments; whole-value bindings
  retain the scrutinee type. Annotation and return diagnostics share that scope.
- Checked lowering annotations retain f32. The existing lowering classifier reads
  that width for calls and projections, selecting the existing f32 rounding path.
  Legacy match temporaries still store both float widths in the f64 storage domain.

The builtin generic payload rule comes from the preserved typed-match design.
Its new pattern syntax is not activated here, and this does not duplicate its
unfinished match lowerer. Parsing and initial type checking remain frontend work.
No AST ownership analysis is retired by this prerequisite. String annotations
remain an incomplete projection, not a substitute for full Types in semantic IR.

## Reproductions and validation

The initial actual-checker regression on parent #8964 accepted concrete-width
errors and rejected a valid f32 receiver. The new diagnostic tests compare with
the Go checker and require the expected diagnostic code, not merely rejection.

The production CLI runtime corpus pins exit 7 independently and checks the Go
interpreter oracle before compiling for x86-64 Linux, ARM64 Linux and Wasm.
On unchanged parent cb6229795, 21 of the first 27 target/scenario combinations
failed: direct calls, call receivers, call and array projections, if joins,
branch-local results and Option joins. Six controls passed. These are real
miscompilations or strict-IR refusals, not self-referential fixpoint differences.
The expanded final corpus has ten scenarios, including mixed-width Result arms.

Targeted validation before publication:

- 39 actual-checker/native-oracle diagnostic cases and 16 structural type outputs
  pass, including branch order, scoped locals, payload return and guard diagnostics.
- All 30 strict production CLI target/scenario combinations pass.
- Existing f32 element rendering and Option-payload tests run through the
  configured execution runner instead of skipping on a non-x86 host. All fifteen
  target/scenario combinations pass.
- Full source-lint and `make lint-all` pass. A final checker differential,
  annotation, resolver, value-block and float regression sweep is running at
  publication; full integration and bootstrap CI remain merge requirements.

QEMU results are correctness evidence only. Earlier failed local runs are retained
as reproductions, including a guard-test fixture corrected from `if` to the
language's `when` syntax. No failing expectation was waived or weakened.

## Controlled size comparison

Both trees were compiled with the identical native bootstrap binary from #8959,
SHA-256 `52aab6c4c723f731cdf3dc6835cfee72991ddbece554973023b4a9e9199ab6e7`,
using `-target arm64-linux`, then linked with `gcc -static -nostdlib` in the
same Linux ARM64 toolchain image. Parent is #8964 cb6229795. The checker input
is `checker.fern`, not the distinct `checker_run.fern` diagnostic driver.
The freshly rebuilt parent checker assembly is byte-identical to the prior
parent measurement, confirming the comparison input.

| Artifact | Parent ELF bytes | Updated ELF bytes | Change | Text change |
| --- | ---: | ---: | ---: | ---: |
| Production Fern CLI | 17,029,752 | 17,033,352 | +3,600 | +2,552 |
| Checker self-test executable | 3,328,472 | 3,331,816 | +3,344 | +2,368 |

CLI section accounting: text +2,552, rodata +144, unwind tables +268,
symbol table +384, symbol strings +238, alignment +14 = 3,600 file bytes.
BSS is unchanged. Every text byte is attributed by the symbol-size comparison
in [the measurement record](measurements/selfhost-precise-floats-2026-09-09.txt).

The growth supplies checked width, scoped joins, generic payload binding and
diagnostics. Shared join/scope logic removes the duplicated branch loop and the
name-only scrutinee helper. No size baseline changed. This is not a performance
speedup claim: compile-time and allocation effects need dedicated quiet native
measurement; compilation during concurrent validation is not such a benchmark.
