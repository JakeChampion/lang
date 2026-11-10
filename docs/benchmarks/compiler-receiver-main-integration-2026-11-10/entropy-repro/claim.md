Claimed by Codex on `perf/index-receiver-union-membership` for `TestSelfHostEntropyAllocationCapacity` only.

Current main `bcd223bae6ab2d8dc14b3b152027fa7be90e6779` reproduces both negative-length failures on x86-64 and QEMU ARM64.
The runtime correctly refuses negative counts with exit 134 and the allocation-size diagnostic.
The fixture still expects an empty allocation.
I will require refusal before the allocator hook and preserve all valid capacity controls.

The existing E009 and other main-red claims remain with their owners.
