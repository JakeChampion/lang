# Skip parameter types for calls with no arguments

Part of #11534 and #8171.

The checker uses parameter types to give argument literals their expected types. For a call such as `next()`, there are no argument literals to annotate. The checker now skips that parameter lookup. It still checks the call and infers its result type.

Two stage-2 compilers built the same sources from `e0ada713b0` for x86-64 Linux. Both runs used Callgrind with `FERN_RC_CERTIFY=1` and `FERN_RC_CERTIFY_MEMO=0`.

The conformance fixture `a_closure_in_a_generic_function_captures_a_function_of_its_type_parameter` calls generic callbacks with zero and one argument. The instruction count fell from 395,640,844 to 395,565,806, a reduction of 75,038 instructions. Both compilers produced the same executable. The executable printed the expected output.

For the full `compiler/checker.fern` compile, the instruction count fell from 49,139,726,581 to 49,116,138,220. That saved 23,588,361 instructions. The fixed checker binaries matched on all six targets.
