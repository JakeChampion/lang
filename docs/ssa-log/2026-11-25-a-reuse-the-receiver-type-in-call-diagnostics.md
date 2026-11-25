# Call diagnostics reuse the receiver type

Part of #11534 and #8171.

When the checker sees a call such as `make().method()`, it checks the type of `make()` to validate tuple member access. It also needs that type to infer a generic receiver from the result type the caller expects. Both checks now use the same receiver type. When the caller supplies no destination type, the checker skips the method lookup and returns before it binds type variables.

Two self-built x86-64 Linux compilers compiled the same `compiler/checker.fern` and standard library from `5b7744639a`. Both runs used Callgrind with `FERN_RC_CERTIFY=1` and `FERN_RC_CERTIFY_MEMO=0`. The second compiler included the receiver change.

| Workload | Instructions before | Instructions after | Instructions saved |
| --- | ---: | ---: | ---: |
| Compile the checker to an x86-64 executable | 49,139,306,473 | 49,137,622,905 | 1,683,568 |
| Check the generic array method-chain fixture | 1,022,294,083 | 1,022,089,914 | 204,169 |

The fixed checker source produced byte-identical executables for x86-64 Linux, ARM64 Linux and wasm32 WASI. The 41 receiver and generic-call fixtures compared with `-check` had identical diagnostics and exit statuses.
