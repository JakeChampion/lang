# Allocator ABI review

The allocator path now uses one argument explicitly.
The normal tail-call path retains each function's scalar and lane ABI.
The ARM64 comment describes both word and byte displacements.

The focused combined suite passes in 38.231 seconds.
It covers both native targets, packed bytes, the allocation census, hardening, and the lane ABI.
Bootstrap and driver-size checks for this source are in progress.
The parent archive retains the measurements for the previous source.
