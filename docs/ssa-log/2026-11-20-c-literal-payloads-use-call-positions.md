# Literal payloads use call positions

Issues #11534 and #8171.

The checker records the type of a composite constructor argument or the
receiver of a call on an array literal. A rewrite later looks up that type
by the call's line and column and the argument's position. The receiver
uses position -1. The table now stores and compares the three integers.
As before, a lookup returns the first entry that matches all three.

## Measurements

Two self-built x86-64 compilers compiled and linked the same inputs from
main at `05556d62698e`. Callgrind ran with certification enabled and
its on-disk cache disabled. Separate allocation runs used the settings
from `scripts/selfhost-alloc-bench`, with certification disabled.

| Input | Instructions on main | Instructions with change | Allocations on main | Allocations with change |
| --- | ---: | ---: | ---: | ---: |
| `wider_use_settles_a_nested_payload_literal` | 199,139,538 | 199,117,330 | 416,245 | 416,175 |
| `integer_literal_in_an_append_on_a_float_array_is_a_float` | 451,870,138 | 451,814,083 | 770,991 | 770,821 |
| `compiler/checker.fern` | 48,409,806,351 | 48,409,793,075 | 55,079,444 | 55,079,444 |

The two fixtures exercise the payload and receiver paths. They used
22,208 and 56,055 fewer instructions. The whole-checker difference was
under 0.00003%. All three allocation runs on each compiler ended with
zero live bytes. The checker executables matched byte for byte on
x86-64 Linux, ARM64 Linux and wasm32 WASI.
