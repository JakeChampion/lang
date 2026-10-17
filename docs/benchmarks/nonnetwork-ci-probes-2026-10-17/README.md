# Compiler CI probes after scalar returns and runtime retirement

The one-to-four-field scalar return ABI removes the allocation several older
heap and allocation-contract probes intended to observe. Those probes now
use five-field records. Their allocation, release and E068 assertions remain
active, including balanced sanitizer counts and nonzero bounded high-water.
The independent guarded-extraction proof fixtures likewise retain boxed
children rather than receiving a scalar ABI result.

Assembly assertions inspect the program function. Runtime helpers may contain
a release call even when the program's constant aggregate allocates nothing.
The static-box census records the scalar return ABI's zero allocations for
fresh and unsigned scalar results, preserving all other exact counts.
The retained-body proof's independent x86 register boundary includes all
fourteen current integer homes. The production proof rules are unchanged.

The IR census and spec remove the retired struct_copy operation. The semantic
source snapshot removes three redundant field reads from the existing common
subexpression pass, with the corresponding uses rewritten to the earlier
values. No pass or check is disabled.

On main bbadbc3ff plus these probe corrections, the focused allocation,
release, pattern, IR-inventory and lift tests pass in 62.545 seconds.
The E068 fixture and diagnostic-index checks pass in 23.168 seconds.
The combined proof run (441.155 seconds) completed the guarded-extraction,
retained-body, default static-box census and semantic-source snapshot checks.
It found one additional small-result fixture in typed extraction (exit 130).
After that selected-result fixture and its sibling were widened too, the
complete typed-extraction driver passes on ARM64, x86-64 and WASM in 54.452
seconds, retaining sanitizer/leak checks and all negative controls. The raw
combined log retains that earlier failure; typed-proof-tests.log records the
corrected driver. The other drivers were unchanged and were not repeated.

The strict performance ceiling updates are the exact improvements measured
on main bbadbc3ff in Actions run 39065818386, jobs 117494191737 and
117494191770. PR run 39071082430 independently reproduced the x86 values
(job 117508992605):

| Metric | Previous ceiling | Measured ceiling |
| --- | ---: | ---: |
| x86_64/enum_match.text | 935 | 891 |
| x86_64/enum_match.ir | 51,750,055 | 50,850,037 |
| aarch64/enum_match.text | 1,128 | 1,085 |
| x86_64/http_hello.text | 16,813 | 14,458 |
| aarch64/http_hello.text | 17,130 | 14,914 |

All tolerances remain unchanged. The HTTP rows are incidental compiler
benchmark measurements; no HTTP implementation or HTTP issue is changed.
These results do not establish that every lane on main is green.

Refs #12593, #12616 and #12617.
