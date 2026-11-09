# Method and module call roots

An imported receiver control exposed a second regression from #14754.
Calls such as `lib.make().run()` retained an unrelated free function named `run`.
That free function called `subprocess`, so the valid WASI program reported E066.
The qualified main compiler reproduces all three failed imported receiver cases.

Known receiver calls now add qualified method roots and generic method roots only.
An unresolved receiver still adds the free-function call root.
This preserves module-qualified calls before module resolution.
Method arity, operator shape, and conservative generic dispatch retain their separate rules.

The supported compiler build passes.
Its core SHA-256 is `54a9241b7cfd3e62d0f9493bcb26c5641cbc1f9c150c0f5331329b66d7d900ac`.
The imported receiver control now passes its call, inferred, and element cases.
New cases keep a module-qualified free function and report E066 for a reachable module-qualified capability call.
The entry-module capability control still reports E066.

The primitive display controls pass native Linux and WASI execution, plus Android ARM64 compilation.
Generic display, trait dispatch, drop, free-function, display argument, and complexity controls pass.
The method-arity control and the trait-self control also pass.

All 40 native static measurements pass the strict performance gate.
All 120 static measurements complete across x86-64, ARM64, and WASI.
Two prior self-host size warnings remain, and their limits stay unchanged.
The four FLAC encoder suites and three M4A ALAC suites pass native sanitizers and WASI.
All seven compile as Android ARM64 shared libraries.

No performance limit increases.
Valgrind is absent, so local dynamic instruction counts remain unmeasured.
Full CI remains required.

Issues: #14770 and #14772. Parent: #14509.
