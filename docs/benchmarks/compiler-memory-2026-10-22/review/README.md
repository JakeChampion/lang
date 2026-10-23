# Allocator ABI review

Source: 06e62933000bb1829decbf8fd5009c99d0d49fcd.
Compiler and stdlib source SHA-256: 08b46193b4dd5dea3920e70bd88e61f90a81099dfe1f8b7c3c4a17b56832faca.

The allocator path now uses one argument explicitly.
The normal tail-call path retains each function's scalar and lane ABI.
The ARM64 comment describes both word and byte displacements.

The focused combined suite passes in 38.231 seconds.
It covers both native targets, packed bytes, the allocation census, hardening, and the lane ABI.
Stages two and three match at 14,159,904 bytes, SHA-256 8f88f1647673290606699ae8b3c6ebb399332e838a139132da5e6469e29db543.
All eleven strict size rows pass without baseline changes.
The report uses the measured bootstrap row and ten stock-driver builds.
Those builds pass in 61.348 seconds.

The final stage-three compiler reproduces all four assembly outputs from the earlier combined allocation-profile control.
The raw inputs and the control remain in the parent archive.
The parent archive retains the earlier measurements and their source scopes.
