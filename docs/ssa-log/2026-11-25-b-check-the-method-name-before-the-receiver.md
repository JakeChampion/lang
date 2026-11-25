# Check the method name before the receiver

Part of #11534 and #8171.

Field calls such as `make().method()` can need a different method name in the generated code. Only `close_unchecked` on a `Reader`, `Writer` or `Dir` lowers to `close`. The checker now tests the method name first. For other methods, field annotation supplies the receiver type without a second walk over the receiver expression. Calls to a function stored in a field still carry the full callback signature.

Two stage-2 compilers built the same sources from `cb7918ea9e` for x86-64 Linux. Both runs used Callgrind with `FERN_RC_CERTIFY=1` and `FERN_RC_CERTIFY_MEMO=0`.

| Source | Instructions before | Instructions after | Instructions saved |
| --- | ---: | ---: | ---: |
| `conformance/cases/generic_array_method_chain/main.fern` | 872,917,101 | 872,047,972 | 869,129 |
| `compiler/checker.fern` | 49,148,654,715 | 49,136,939,881 | 11,714,834 |

Both compilers produced the same executable for the method chain. The stage-2 compiler with the change also reproduced itself byte for byte.
