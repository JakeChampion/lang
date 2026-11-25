# Reuse the initializer type for an inferred local

Part of #11534 and #8171.

When the checker annotates a local such as `let f = handlers[i]`, it computes the type of the initializer. It now uses that type to bind `f` and record its callable signature. If annotation supplies no type, the checker infers the type from the original expression. These rules give a recursive local function its own name in scope.

Two stage-2 compilers built the same sources from `259993fefd` for x86-64 Linux. Both runs used Callgrind with `FERN_RC_CERTIFY=1` and `FERN_RC_CERTIFY_MEMO=0`.

| Source | Operation | Instructions before | Instructions after | Instructions saved |
| --- | --- | ---: | ---: | ---: |
| `conformance/cases/closure_field_rebind/main.fern` | Compile | 45,877,214 | 45,867,513 | 9,701 |
