# Native memory code generation on main c96d9524d

Base: c96d9524de7e5d90d1db10ee63dcd48f51e16e25.
Source: 9cdaa5686786a46942bdae989bbcbd8472b0af6f.
The merge retains main's scalar storage for never map columns.

The focused suite passes in 137.475 seconds. It covers constant raw-word
indexes, allocator tail calls, large donor release, ARM64 hardening, the feature
census, lane-call refusals, and the full semantic contract tests. Both native
writers, GAS, sanitizer modes, and ARM64 hardening pass. The semantic tests
include main's new never map column cases.

The main-c96d directory contains the log and command. The earlier complete
bootstrap, eleven strict size checks, and broader suite retain their recorded
main-12625 source scope.
