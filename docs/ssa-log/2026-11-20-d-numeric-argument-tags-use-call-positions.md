# Numeric argument tags use call positions

Issues #11534 and #8171.

The literal checker records type spellings for generic call arguments and
for enum constructor arguments whose widths settle later. It now keeps the
call's line, column and argument index as integers. Float reads still use the
expression's position and shape. Each table keeps a tag only when all the
entries at that key agree.

Two self-built x86-64 Linux compilers built the same sources from main at
`95e888217cb6`. Callgrind counted instructions with certification enabled
and the on-disk certification cache disabled. Separate leak-check builds
counted allocations with certification disabled. All allocations were freed.

| Input | Instructions on main | Instructions with change | Allocations on main | Allocations with change |
| --- | ---: | ---: | ---: | ---: |
| Three inferred generic calls | 22,037,198 | 22,014,972 | 56,656 | 56,602 |
| `generic_float_literal_arg` | 22,899,317 | 22,899,585 | 58,725 | 58,725 |
| `integer_literal_in_an_append_on_a_float_array_is_a_float` | 451,963,311 | 451,903,964 | 770,836 | 770,606 |
| `generic_enum_payload_settles_its_parameter` | 952,624,894 | 952,695,193 | 1,992,737 | 1,992,737 |
| `compiler/checker.fern` | 48,413,599,333 | 48,414,079,245 | 55,090,886 | 55,090,886 |

The first input calls one generic function with unsuffixed integer arguments.
Its three destinations require distinct types.

```fern
function choose[T](a: T, b: T, first: boolean): T {
  if (first) { return a; }
  return b;
}
function main(): i32 {
  let a: i64 = choose(2147483647 + 8, 2, true);
  let b: f64 = choose(1, 2, false);
  let c: u16 = choose(65535, 2, true);
  if (a != 2147483655i64 || b != 2.0 || c != 65535u16) { return 1; }
  return 0;
}
```

The three-call example used 22,226 fewer instructions and 54 fewer
allocations. The array-append fixture used 59,347 fewer instructions and
230 fewer allocations. The float-literal and enum fixtures used 268 and
70,299 more instructions respectively, with the same allocation counts.
Building the fixed checker used 479,912 more instructions (0.00099%) and
the same 55,090,886 allocations. Its output matched byte for byte on
x86-64 Linux, ARM64 Linux and wasm32 WASI.
