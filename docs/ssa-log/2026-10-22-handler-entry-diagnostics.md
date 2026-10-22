# Handler entry diagnostics

A handler-only program that needs a serve loop must import `std/serve`.
Both front ends report E001 at `handle` when the import is absent. The check
also accepts flat fixtures that provide the serve helpers directly.

The compiler checks the dependency before it creates the implicit main.
Check, interpreter, and compile modes report the same message and position.
An explicit main controls its own dependencies. A WASI HTTP component uses
its incoming-handler export and needs no serve loop.

An executable without a main or handler now reports a positioned E001 before
assembly. Source checks, assembly output, shared libraries, script entries,
and Wasm guest exports keep their entry rules. This resolves #12602.
