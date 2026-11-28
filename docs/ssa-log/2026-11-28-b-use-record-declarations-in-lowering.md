# Use the checker declaration for record lookups

Part of #11534 under #8171.

A record literal needs the record name before it can place the fields. A dynamic trait lookup needs to know whether a record exists.
Both lookups used to resolve every field type. They now read the declaration from the checker scope.
The declaration filter skips generic templates and enum payloads as before.

Field reads, record literals and record updates still get resolved field types from `record_sig`.
It looks up the resolved record name because template substitution can change that name.
For a generic instance, it resolves the declared fields in scope before it substitutes the instance arguments.

## Compile cost

Callgrind counted x86-64 instructions with verification on. Both stage-2 compilers used the same paths and arguments.
The checker input came from main `8558080db5ca`. The diff command came from main `426740dbc0da`.
Both compilers produced identical binaries for every row with binary output.

| Fixed input | Main instructions | Branch instructions | Difference |
| --- | ---: | ---: | ---: |
| Full checker compile | 97,190,506,227 | 97,186,359,309 | -4,146,918 |
| Checker, check only | 15,089,488,546 | 15,084,537,482 | -4,951,064 |
| Optimized diff command | 10,285,955,115 | 10,284,651,459 | -1,303,656 |
| 1 record field | 37,567,549 | 37,566,583 | -966 |
| 16 record fields | 41,693,031 | 41,685,564 | -7,467 |
| 64 record fields | 50,943,833 | 50,916,125 | -27,708 |
| Scalar control | 18,517,122 | 18,517,122 | 0 |

The main compiler allocated and freed 74,936,218 objects on the full checker compile. The branch compiler allocated and freed 74,929,908.
For the 64-field fixture, the counts were 117,219 and 117,207, respectively.
Both stage-2 compilers reproduced themselves. The branch executable grew by 320 bytes.

## Verification

The full conformance reports matched in all 6,291 rows. They contained 4,473 identical binaries and 1,818 matching refusals.

Temporary storage filled during the first run. Both compilers rebuilt the 179 affected or new fixture and target pairs.
The report SHA-256 was `568d028dfcc8b20defe7c82e31d80b572f231ab9b3643e86648347303b5c5106`. The 530 expected-error JSON diagnostics matched.
Six checker binaries, the HTTP component and both freestanding assembly outputs matched.

The 66 record comparisons covered construction, field access, updates, generic types and dynamic dispatch on three targets.
Another 42 comparisons included allocation checks and sanitizer builds on both native targets.
They covered two imported records with the same name and different fields.

Nine more binary pairs passed the counted-record and full-update driver fixtures on the three targets.
Each block of 1,000 partial or full record updates allocated zero objects in the ordinary build.
The sanitizer builds freed every allocation.
On x86-64 Linux and ARM64 Linux, both compilers passed the three new GNU diff comparisons from #17541.

Both compilers passed all 115 ownership certifier cases. The 2,379 per-function mutation records matched.
Each compiler refuted 5,387 mutations, weakened 229 and missed 229.
The native driver had 739 unproved values on both builds. The WASI checker had 71 on both builds.
Both stage-1 compilers copied 57,840,544 shared-array bytes in 1,006,169 copies on each source tree.

## Integration with main

The branch includes the checker ownership change from #17539 at main `b615fba2d152`.
Both integrated compilers accepted its valid program on three targets.
They refused its array and map cases with the expected E050 messages.
All 532 expected-error JSON diagnostics matched.
The 6,300 conformance rows matched, including 4,476 identical binaries and 1,824 matching refusals.

Both integrated stage-1 compilers counted 739 native driver and 71 WASI checker values as unproved.
They refuted 5,389 mutations, weakened 229 and missed 229. All 2,380 per-function mutation records matched.
Each compiler measured 57,847,600 shared-array bytes in 1,006,254 copies on either source tree.

## Reproduce the record fixture

This generator prints a record with the requested number of fields. The program constructs the record, updates it and reads its first field.

```python
import sys

count = int(sys.argv[1])
fields = ", ".join(f"x{i}: i32" for i in range(count))
values = ", ".join(f"x{i}: {i}" for i in range(count))
print("struct Row { " + fields + " }")
print("@noinline function bump(r: Row): Row { return Row { ...r, x0: r.x0 + 1 }; }")
print("function main(): i32 { let r = Row { " + values + " }; let v = bump(r); return v.x0 - 1; }")
```
