# A method with a branch joins the once set

2026-11-25. Each target. The change is in `semsource`. Refs #14440.

## The change

`semsource.spliceable_body` is deleted. A method that only direct calls
reach now joins the once set when its body has more than one block. Its body
is dropped when each caller took it, as for a private free function.

The rule came from #13990. A branch in a spliced body puts the store-back
of the caller after a join. The plan did not take an element across that
join. #13990 is closed. #17027 and #17081 removed the two last costs that a
splice of each method showed in the HTTP/3 probes.

`TestSelfHostSpliceBranchMethod` holds a method with three blocks and one
caller. On main `9632fa79` the method stays a function. After the change it
is spliced into its caller.

## Measured

HTTP/3 probes, x86-64, allocations a request:

| Probe | main `9632fa79` | after |
| --- | --: | --: |
| `TestSelfHostServeAllocsPerRequestH3` | 49.69 | 47.69 |
| `TestSelfHostServeAllocsPerRequestH3Mux` | 28.67 | 27.65 |

The ceilings go from 50 and 29 to 48 and 28.

The compiler, x86-64. The stage-2 compilers of main and of the change each
compile the sources of main. Three runs each, alternated:

| | main | after |
| --- | --: | --: |
| user time | 66.9 to 80.6 s | 67.1 to 68.1 s |
| peak RSS | 2,939 to 2,943 MB | 2,965 MB |
| stage-2 binary | 34,071,568 bytes | 34,133,696 bytes (+0.18%) |

The time does not change outside the noise. The peak RSS grows by 0.9%
and the binary by 62 KB.
