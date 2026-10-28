# Capability grants and the OS sandbox

Status: audit for [SEC-038](https://github.com/JakeChampion/lang/issues/13540).
Parent: [security epic](https://github.com/JakeChampion/lang/issues/13502).
The grant design is `docs/PACKAGE-CAPABILITIES-BRIEF.md`, the manifest in `docs/PACKAGES.md`, and the process sandbox in [SANDBOX-AUDIT.md](SANDBOX-AUDIT.md).

## Two mechanisms

A capability grant is a compile-time rule about a package.
A dependency in `fern.toml` carries `capabilities = [...]` from the vocabulary `env`, `fs`, `net`, `random`, `subprocess` and `time`.
The checker computes which builtins each package reaches through the closed import graph and the call graph. `E070` refuses a package that reaches a capability it was not granted.
Without the key the reaches print as warnings.

Grants attenuate.
A governed dependency can grant its own dependencies at most what it holds, and an amplifying grant is a manifest error at load time.

The sandbox is a run-time rule about a process.
`FERN_SANDBOX=1` installs a seccomp allowlist of the system call numbers the program contains, after `no_new_privs`, and a denied call ends the process with `SIGSYS`.
`-heap-limit` bounds the arena, `rlimit_nofile` reads the descriptor ceiling, and `setuid`, `setgid` and `setgroups` drop privilege.

The two compose.
A grant answers what a dependency can reach, before the program exists.
The sandbox answers what the process can do, whatever its code reaches.
A package with no `net` grant cannot compile a call to `tcp_connect`. A sandboxed process whose code never contained `connect` cannot make that call, even through a forged pointer.

## Build-time code

Fern runs no package code at build time.
There are no build scripts, no hooks and no procedural macros, and a manifest holds data only.
The compiler reads sources and writes an image, so a sandbox for build scripts has nothing to confine.
`-doctest` and `-run` execute code only on request.

## Verification

| Test | What it pins |
|---|---|
| `TestEnforce`, `TestAnalyzeTransitiveAcrossModules`, `TestAnalyzeClosureDefinitionSiteAttribution`, `TestAnalyzeFunctionValueEdge` | the Go analysis: a reach through a chain of modules, through a closure and through a function value is charged to the package that wrote the code |
| `TestTraitDefaultChargesTheDeclaringPackage`, `TestTraitDefaultChargesTheDeclarerThroughAnIntermediate` | a trait default method is charged to the package that declares it |
| `TestSelfHostCapsRules`, `TestSelfHostPackageCapabilityDifferentialX86_64`, `TestSelfHostCapsTraitDefaultDifferentialX86_64`, `TestSelfHostCapabilitiesReportAgreesWithNative` | the self-host checker refuses the same programs with `E070` and reports the same reaches as the Go analysis |
| `TestInventoryCoversCheckerRegistry`, `TestSelfHostRegistryFullyClassified`, `TestUngatedBuiltinsAreNotCapabilities` | every builtin is classified, in both compilers, so a new builtin cannot slip past the gate |
| `TestSeccompFilterDenies`, `TestSeccompNoNewPrivsBlocksSetuidEscalation` | the sandbox kills a denied `execve` and blocks a setuid escalation |

## Remaining limits

- The vocabulary is coarse. `net` is every host and every port, and `fs` is every path.
- A grant is static. A program cannot attenuate a capability at run time or hand a dependency a single file.
- The sandbox filters by system call number alone, and only on x86-64-linux.
- A foreign function reached through `__c_call` is outside the vocabulary. The raw builtins are the unsafe surface of [UNSAFE-INVENTORY.md](UNSAFE-INVENTORY.md).
- A resource quota exists for the arena and the descriptor count. There is no CPU time or child process quota.
