# Reuse the declarations of mutable closure captures

When a closure shares mutable scalar locals, the compiler rewrites those locals as cells. The lifting pass used to walk the enclosing function once for each boxed name and format its first declaration as a string. The rewriter formatted the name, line and column again to distinguish a declaration from a later shadow.

The lifting pass now collects those declarations in one walk, using the existing rule for descending into a capture frame. Each boxed name has a slot containing its first declaration, or an empty slot if no declaration was found. The expression and statement rewriters share the slots and still compare the full name and source position. A boxed parameter with an empty slot still gets its cell at function entry.

## Escaping mutable captures

Each test function initializes its locals to 2 and returns a closure that increments every local and sums them. The caller invokes the closure twice and checks the sum of the results. The table counts x86-64 Linux instructions with Callgrind during stage-2 compilation of the same input on base `7b4a6685e06d`. Each pair emitted the same executable.

| Captured locals | Base instructions | With shared declarations | Fewer instructions |
| ---: | ---: | ---: | ---: |
| 1 | 38,019,317 | 38,017,479 | 1,838 |
| 8 | 44,218,446 | 44,197,439 | 21,007 |
| 32 | 69,980,355 | 69,782,195 | 198,160 |

The three programs also passed with leak checking on x86-64 Linux, ARM64 Linux and WASI. Both compilers produced identical outputs on each target. The closures allocated and freed 2, 9 and 33 objects respectively. A separate stage-1 control with 32 locals and a directly invoked closure took 49 more compile instructions.

## Fixed checker and conformance

Using both stage-2 compilers on the same `compiler/checker.fern` and standard library from `7b4a6685e06d`, the full x86-64 Linux compile used 94,871,923,116 instructions on base and 94,871,938,274 with shared declarations, an increase of 15,158 (0.000016%). Both emitted the same binary and each allocated and freed 74,297,990 objects. These runs used `FERN_IR_VERIFY=quiet`, `FERN_RC_CERTIFY=1` and `FERN_RC_CERTIFY_MEMO=0`.

Both stage-2 compilers reproduced themselves. All 6,258 conformance results matched on x86-64 Linux, ARM64 Linux and WASI, including 4,458 byte-identical binaries and 1,800 refusals. All 526 expected-error JSON diagnostics matched, as did fixed checker binaries on six targets, the WASI HTTP component and both freestanding assembly outputs.

The focused suite matched all 27 capture program and target combinations, including captured parameters, shadowing and reassignment. A separate program with a captured parameter and a same-line destructuring shadow returned the expected result and freed all three allocations on each target. All 39 ownership checks passed.
