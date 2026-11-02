# Nested record tokens

## Final source on merged security main

The [final parent record](final-security-main/qualification.json) uses main `1b032a0c154cddb26960e41d8681909c95427ce8`.
Its complete tree matches the security PR head, which passed 74 non-Netlify checks with 24 legitimate skips.
Those results qualify the parent. The issue PR still requires its own full CI.

Every compiler and standard-library hash matches the separate post-root source binding.
Recompilation produces the identical issue compiler, `ee1c9ebb069764c8c73c70ea0412b88fc6c747c4cf58f5dacaeadf1e6e26503e`.
Five additional runtime-root and per-module controls pass without skips.
The owner's empty-image sanitizer control remains intact.
The broad campaigns below retain their original source scopes and compiler identities.

## Private integration with the security parent

The [security integration records](pending-security-c3d/qualification.json) use pending PR #14019 at `c3d5987c6b` as their parent.
They preserve the managed heap, constant-data protection, writable empty-array exception and exact retained-body proof.
The later runtime-root declaration in `5af8c3fe10` is outside this snapshot.
Publication requires the actual merged security parent and its final integration checks.

Compiler stages 2 through 4 match `ee1c9ebb069764c8c73c70ea0412b88fc6c747c4cf58f5dacaeadf1e6e26503e` at 23,966,033 bytes.
All 23 Linux roots and three Darwin roots pass without skips.
All nine retained-body controls pass on ARM64 Linux, QEMU x86-64 and Wasm, with nonzero balanced allocations and zero live bytes.

Source checks cover five runtime targets and 90 drivers. Lint and format checks pass.
Compiler complexity is 18,302 excess, with worst score 223. The original ceilings remain unchanged.

The full 39-benchmark campaign passes all 78 ARM strict, 39 x86 static strict and 117 advisory metrics.
All observable outputs match the exact security parent.
Compared with that parent, ten emitted rows decrease, and no Wasm emitted row increases.
The native `perceus_cfold` rows change from 1,922 to 1,927 on x86 and from 2,181 to 2,188 on ARM.
Both remain within the original strict limits. No baseline increases.

Native ARM Callgrind controls use the same executable name and arguments for both compilers.
An eight-step pilot precedes the 10,000-step campaign. Collection covers each update function and its callees.

| Shape | Parent instructions | Candidate instructions | Parent allocations | Candidate allocations |
|---|---:|---:|---:|---:|
| A | 4,202,038 | 1,672,190 | 3 | 3 |
| B | 4,362,022 | 1,832,174 | 3 | 3 |
| C | 2,652,099 | 1,992,158 | 3 | 3 |
| H | 2,812,083 | 2,152,142 | 3 | 3 |
| P | 5,692,243 | 3,912,127 | 10,002 | 3 |
| D | 5,312,241 | 3,532,125 | 10,002 | 3 |
| S | 4,551,721 | 1,952,023 | 2 | 2 |

Instruction totals cover 10,000 calls, rather than elapsed time. Allocation totals cover the whole unique-input fixture.
The separate sanitized semantic controls check shared inputs and zero live bytes on all four targets.

Matched compiler-cost runs repeat 46,231,900 parent allocation events and 46,406,334 candidate events.
Heap-bump bytes change from 542,272,904 to 542,263,928.
All events balance, with zero live bytes. These counters make no latency claim.
Earlier evidence below retains its original source scope.

## Final source on main 5240

The [final records](final-main5240/qualification.json) include both PR #14016 optimizations and the CLI complexity repair.
All four compiler stages match `6f37bd429227c87016079d9fe5ef40fed48b45e312c6996a86e96aab1294ba55` at 23,899,697 bytes.
All 19 Linux roots and three Darwin roots pass without skips.
The source gate covers five runtime targets and all 90 drivers. Lint and format checks pass.

All 39 benchmarks complete again. The 78 ARM strict rows, 39 x86 static strict rows and 117 advisory target rows pass their limits.
All 39 ARM observable outputs match main. No limit increases.

The five new Wasm size increases disappear when dead witness clears are removed.
The controlled pilot and full run preserve every output. Carried donor locals still clear after consumption.

The matched compiler-cost control repeats 46,231,851 allocation events for main and 46,406,285 for the candidate.
Final heap-bump bytes are 542,272,904 and 542,263,928 respectively.
Both compilers balance their events and leave zero live bytes. These counters make no latency claim.

## Earlier source on main 711

These records qualify the issue #12795 compiler on merged main `71121667b0d94ad8895847fb2584637a8a3b0b57`.
They exclude later PR #14016.
The fixed-point record binds every compiler and standard-library input to the stable producer.
Stages 2, 3, and 4 match exactly.

All fourteen Linux regression roots pass without skips.
The separate token-region driver test and all three Darwin roots also pass without skips.
The wide fixture checks unique and shared values, fixed and runtime loops, and constant unique allocation counts.
The source gate checks five runtime targets and all 90 drivers.

The later seven-shape controls also cover decoder-first and scalar-only updates.
All three roots pass again in 75.891 seconds on Linux and 42.417 seconds on Darwin, without skips.
The manifest binds the final fixture and Go tests to their hashes.

All 39 benchmarks complete.
The strict comparison covers 78 ARM static and dynamic rows and 39 x86 static rows.
All 39 ARM observable outputs match main.
Two fresh runs repeat both lower ARM red-black-tree counts exactly.
Only those two limits decrease. No limit increases.

The 117 target-output rows retain the existing advisory policy.

The compiler-cost control repeats its final counters on both checker runs.
Allocation events change from 46,136,932 to 46,317,322.
Final heap-bump bytes change from 542,272,840 to 542,442,200.
Both runs balance all allocation events and leave zero live bytes.
These counters make no latency claim.

The earlier wide-cost record retains its earlier private source scope.
Public log copies omit extra trailing blank lines, while the manifest retains both original and public hashes.
