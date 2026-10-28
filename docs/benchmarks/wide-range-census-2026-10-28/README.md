# Wide range-loop census qualification

Source: `73f407b489ea3f66d55bc3b3329a474dff070762`.
Parent source: `047aa1c3356a502eaf90d6ce13e29346b12cc4d5`.
The capacity table and class lookup replace two manual index loops with integer range loops.
The unchanged feature-census ceiling remains 5200.

The original helper produced 5202 manual increments.
The changed helper produces exactly 5200.
The four legacy files and all three generated reference files retain their original bytes.
All 4474 expected integer results and the declared mathematical domains remain unchanged.

The clean standard build completed its smoke and version checks with exit zero.
The packaged CLI identifies source `73f407b489ea`.
The raw compiler matches the previously qualified current compiler byte for byte.
The helper remains separate from the production decoder and machine.

Three direct workers passed all 4474 reference rows with nonzero balanced zero-live censuses.
The stock arithmetic test and unchanged complexity gates passed seven Go test nodes without skips.
Two private defects compiled and then failed with the required boundary message.
The defects omit the first capacity and the final class lookup index.

The full source gate passed 25 Go test nodes without skips.
It also passed all five runtime target sets, compiler and standard-library checks, and 81 driver checks.
The fresh Go oracle from source `a3296b1a5694a7d456bb3d8d526d7bf381a09b8d` checked and interpreted the changed driver.
Its Go implementation and embedded standard library match this source exactly.

The first fresh Go test link exhausted the original temporary filesystem before any test ran.
A provisional before and after probe omitted Go test DWARF data and retained the same census assertions.
The next complete attempt also exhausted temporary build space before its Go tests ran.
The final complete checks use the normal Go linker profile on a separate provided executable filesystem.

The temporary directory and cache use new aliases under `/tmp`.
The aliases point to `/dev/shm/image-feature-census-go` without a mount or permission change.
The previous caches, worktrees, source files, evidence, and Git objects remain intact.
Resource failures remain separate records and do not count as passes.

No allocation baseline, threshold, validation selector, mathematical tolerance, or reference expectation changes.
The earlier complete image campaigns retain their separate source identities.
The raw production compiler remains byte-identical, so this helper-only change does not repeat those image campaigns.
Actual CI recovery and all actual M5 and epic gates remain open.

The initial audit command passed 218 nodes and failed one exact helper-output assertion.
The global Go verbosity flag added a build-status line to that helper output.
The failed-only retry cleared that flag and passed the unchanged assertion.
The coverage record proves all 219 unique nodes passed, with no repeated successful node and no missing node.

The original audit command remains failed in the raw evidence.
The remaining reference and diff checks passed after the correction.
The format, Go build, vet, workflow, runtime bundle, and CI self-tests also passed.
