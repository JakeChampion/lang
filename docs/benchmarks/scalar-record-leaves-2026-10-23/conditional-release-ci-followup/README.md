# Conditional release and emitted method checks

The record optimization removes every allocation from the original alias fixture.
The release probe keeps its consumer call with `@noinline` so it still exercises allocated records.
The positive allocation requirement remains unchanged.
All three execution targets allocate five records and release all five, with zero live bytes.
The Go reference and all three targets return 65.

The emitted-method check follows the improved record optimization.
The small record method leaves no body or call when the pass is enabled.
Both remain when the pass is disabled.
Both modes execute on x86-64 and WASI and return 51.

All 59 focused test results pass without failures or skips.
Compiler, library and Go production source remain unchanged from the public base.
The final native compiler and all earlier bootstrap and size evidence retain their exact bytes.
The manifest records the source commit, native source hash, inputs and output logs.
