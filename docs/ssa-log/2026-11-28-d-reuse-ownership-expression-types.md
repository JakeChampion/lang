# Reuse the checked expression type in ownership analysis

Part of #11534 under #8171.

The ownership checker tests whether an expression is scalar. For other expressions it uses the type again to check value records, record literals and call results. Both steps use the same scope.
Scalar literals still return immediately. The other paths now share one call to `check_expr`.

## Compilation costs

Both stage-2 compilers used the same paths and arguments on fixed main `260dcd3587c0`. Callgrind counted x86-64 Linux instructions with verification enabled. Both compilers emitted the same binary for each input.

| Input | Main | Branch | Change |
| --- | ---: | ---: | ---: |
| Full checker | 97,183,678,356 | 97,179,486,024 | -4,192,332 |
| Check only | 15,043,084,243 | 15,038,895,689 | -4,188,554 |
| `owned-01.fern` | 19,029,936 | 19,009,277 | -20,659 |
| `owned-16.fern` | 25,313,343 | 24,986,849 | -326,494 |
| `owned-64.fern` | 46,627,711 | 45,322,545 | -1,305,166 |
| `scalar.fern` | 18,166,988 | 18,166,736 | -252 |
| `struct_literal_parts_move_into_own` | 195,340,036 | 195,192,625 | -147,411 |
| `owned_callback_result_composition` | 876,881,759 | 876,836,119 | -45,640 |
| `own_arg_from_call_with_handle_free_borrowed_param` | 194,799,715 | 194,747,922 | -51,793 |
| `own_param_scalar_helper_result` | 44,095,279 | 44,074,513 | -20,766 |

The numbered fixtures pass a record with a writer and an integer to an owned parameter. The scalar control passes one integer to a function. The last four inputs come from `conformance/cases`.

The full checker allocated and freed 74,883,084 objects on main and 74,882,211 on the branch. Both runs ended with zero live bytes. The 64-record fixture used 83,517 allocations on main and 80,765 on the branch.

## Verification

Each stage-2 compiler reproduced itself at stage 3. The stage-2 executable shrank by 368 bytes. All 6,300 rows from `scripts/selfhost-emit-hashes` matched. The reports contained 4,476 binaries and 1,824 refusals. The report SHA-256 was `64985e018ed9e592d96d486d4f85fe252bddac466d0e71890c2a93dac9a81182`.

All 532 JSON error reports matched. The six fixed checker binaries, the HTTP component and both freestanding assembly outputs matched. Both compilers passed 234 focused target comparisons. Five ownership programs also passed with the sanitizer and leak checker on all three targets. Ten negative controls produced the expected ownership errors.

Both stage-1 compilers passed all 115 certifier cases. The mutation report lost the three refuted cases in `ow_expr_is_scalar`. The other 2,379 records matched. The branch refuted 5,386 mutations, weakened 229 and missed 229.

On the main source both stage-1 compilers measured 57,847,600 shared-array bytes in 1,006,254 copies. On the branch source they measured 57,865,784 bytes in 1,006,273 copies. The pins use the measured branch values. Both builds passed the five strict certifier checks and the two shared-array checks.

The changed checker passed the formatter and both compiler lint ratchets. The feature comparison used the prebuilt repository test executable.

## Reproduce the numbered fixture

The fixture defines a record `Box { w: Writer, n: i32 }` and a function `take_box(own box: Box)` that returns `box.n`.
In `main`, start `sum` at zero. Generate one statement for each integer from zero through one less than the row count.
For integer `i`, emit `sum += take_box(Box { w: stdout(), n: i });` with the integer itself in the field.
Return `sum` minus the sum of those integers. Both compilers returned zero on each target.
