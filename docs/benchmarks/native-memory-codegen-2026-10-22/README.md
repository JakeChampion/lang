# Native word offsets and allocator tail calls

The [integrated validation record](main-12625-validation.md) covers main 12625f42b and the current lane-call ABI.
The measurements below retain their earlier source scope.

Base: d669f557c1e75db6baa7473c4e8323b77400df95.
Source: c140fe8ea9bf8bc189e4cc034599f2e12f0f1baa.
The Go test runner is the current pin, Go 1.27.3.

Both native backends use memory displacements for constant raw-word indexes
from zero through 4,095. Byte indexes keep their existing units.
Large and negative word indexes keep the register path.

A direct return of raw array allocation can also use an allocator tail call.
The capacity uses the native integer ABI. Ordinary function calls keep their own ABI.
Profiling retains call frames. Frame cleanup and ARM64 pointer authentication remain active.

The combined primary suite passes in 175.137 seconds.
It covers raw-word reads and writes, GAS and native images, sanitizer modes,
ARM64 hardening, tail calls, cleanup, spills, constant operands and both Linux profilers.
The same-name floating-point function remains an ordinary call.
Loop reuse includes WASI. Registry, call ABI and complexity checks also pass.

Bootstrap stages 2 and 3 match at 14,062,512 bytes, SHA-256
e0cbe2f0324e18f9434d16844dcd14475efdb6226f7d1dd195929d52dfb2d37d.
Stage 1 differs. The bootstrap record contains all stages and the source fingerprint.
All eleven strict size checks pass with complete coverage and unchanged limits.
The driver build takes 91.205 seconds.

The adjacent logs contain the complete validation evidence.
The separate raw-word and allocator-tail benchmark archives retain their original source scope.
Their formatted probes still produce binaries with the archived hashes.
This validation makes no new whole-compiler performance claim.
