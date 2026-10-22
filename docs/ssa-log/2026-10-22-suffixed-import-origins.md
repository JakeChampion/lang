# Suffixed imports keep their module identity

Issue: #13000. Follow-up to #12980.

The manifest resolver now removes a .fern suffix before the shared file read adds the extension.
A dependency can resolve through a directory whose name differs from its dependency name.

The parser also removes the suffix from standard library provenance after the loader establishes that provenance.
The optimizer therefore recognizes std/ndarray.fern as std/ndarray.
User modules keep their own provenance.

The dependency regression passes in both interpreters and as x86-64 and WebAssembly executables.
The ndarray provenance regression checks the scale and product kernel reports.
It also runs with kernels enabled and disabled on x86-64, ARM64 and WebAssembly.
