# A method of more than one block is spliced

2026-11-25. Each target. The change is in `semsource`. Refs #14440.

## The change

`semsource.spliceable_body` is deleted. A method that only direct calls
reach is now `unnamed` for each body, as a free function is. Before, such
a method was `unnamed` only when its body was one block.

The rule came from #13990. A branch in a spliced body puts the store-back
of the caller after a join. The plan then did not take an element across
that join. #17027 and #17081 removed the costs that the search over the
HTTP/3 server found, so the rule no longer saves an allocation there.

## The layout operations of `std/ndarray` stay calls

The first form of this change (#17091) was reverted in #17175. It spliced
`NdArray.packed`, which has more than one block. `semndlayout` reads the
layout of a value from the names of the `std/ndarray` calls, and it runs
after the splice. With no `packed` call left, the `map` of
`TestSelfHostNdarrayLayoutPropagation/repacked_scale` stayed a call and
was not a vector kernel.

`semndlayout.names_layout` names each trusted function whose call gives a
layout fact. Such a function is not `unnamed`, so the splice keeps its
calls.

`TestSelfHostPairReturn/method` pins the paired return of a method call.
The splice of `Feed.next` removed that call, and the loop allocated 0
times, not 6. The test now marks `Feed.next` `@noinline`, so the call
stays.

## Measured

`TestSelfHostServeAllocsPerRequestH3` and
`TestSelfHostServeAllocsPerRequestH3Mux`, x86-64, allocations a request:

| Build | serial | multiplexed |
| --- | --: | --: |
| main `9632fa79` | 49.69 | 28.58 |
| after | 47.69 | 27.64 |

A compile of `compiler/checker.fern` to an x86-64 executable gives an
image 960 bytes larger. The compiler itself is 62,128 bytes larger.

## Next lead

The HTTP/3 serial probe has 47.69 a request. The census
(`TestSelfHostServeAllocsCensusH3`) names the boxes that are left.
