# Wasm abort cause lines

Issue #12956 reports silent Wasm aborts.
Native executables and both interpreters print a cause before they stop.
Wasm printed only the host trap trace.

Wasm now uses the shared cause text for array and string bounds, slice ranges, allocation sizes, and memory exhaustion.
The reporter uses static message bytes and low scratch memory.
It calls the host write directly, so it cannot request another allocation after the allocator fails.
The reporter retains the trap and status 134.

Components with allocation or index guards import stderr.
The command emitter uses the world composer to connect that import.
HTTP components also request the host stream when their runtime can abort.
Pure scalar components can retain their import-free form.
Preview1 cores use the existing fd_write interface.
Raw cores without a host retain their trap stubs.

The shared abort table covers negative indices, updates, string indices, unchecked string slices, array slices, and negative allocation sizes.
It runs on x86-64, ARM64, and Wasm.
A constrained-memory test proves that a failed memory.grow still prints its cause.
An HTTP handler test proves that a proxy trap also prints its cause.

The component integration tests now run the world composer as Wasm.
A shared assembler replaces the duplicated drivers that assumed a fixed import list.
Native and Wasm composition still produce identical bytes across the host-interface categories.
The fixed stdout template retains a scalar putchar test.
