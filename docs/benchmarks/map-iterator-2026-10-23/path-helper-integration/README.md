# Map iterator checks after the parser fixes

The source commit is `fc81935c68d878b7577f2479c627b1b72548c793`.
The native checkpoint is `19551ede188ee36a55578b4c59eddf1778c39a69`.
The later merge adds only the OS validation evidence.
The full compiler and library source hash remains unchanged across that merge.
The path helper extraction produces identical compiler stages before and after the change.

All 98 focused test results pass across two packages without failures or skips.
They cover iterator protocols, imported methods, reclamation, checker errors and the repository limits.
All 88 matrix commands pass with the expected exit status.
Both interpreters agree with all three execution targets.
Four target builds pass, with Darwin checked by compilation.
All 24 runtime allocation counts balance with zero live bytes.

The allocation profile compares the updated OS compiler with this compiler.
The same direct-loop input uses 60000 allocations at 10000 iterations on both compilers.
This result holds on x86-64, ARM64 and WASI.
The owned iterator protocol uses 420000 allocations in the separate protocol row.
All nine profile allocation counts balance with zero live bytes.
The report retains all 18 build and execution commands.

The released stage0 pin produces byte-identical stages 2 and 3.
Their SHA-256 is `9b9d2d318938c173e624382e94d77e69c8d633f1a36330cc9970e0da4933b042`.
All 11 strict driver size checks pass without baseline changes.
Go vet and the changed-file format checks pass.

Go production source matches the earlier oracle checkpoint `ac9bd97b29315fca1d987d701b2b74c3e878567c`.
The earlier 4965-case compiler sweep and 3020-case Go sweep retain their recorded source scopes.
The current manifest records all 497 compiler and library source files.
The archive retains all 214 matrix and profile inputs and output logs.
