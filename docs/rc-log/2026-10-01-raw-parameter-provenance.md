# Raw parameter ownership at function entry

The x86-64 census comparison began reporting four transferred-unit leaks
in `__map_dec_value` and `__map_free_val_cell`. Both helpers take a raw
`usize` value. Map metadata determines whether that word is an unboxed
scalar, a string or array pointer, or a separately allocated cell. Their
early returns are valid when there is no owned pointer to release.

The lift preserved register width in `ParamAddrs`, which deliberately
includes `usize`. The ownership solver correctly found a possible release
and classified the parameter as consumed. `UnitsOf` then combined those
two facts into an unconditional owned unit at entry. That last inference
was unsupported: width and possible consumption do not establish the
provenance of an erased word on every path.

The lift now records raw pointer-width integer parameters separately.
A consumed raw parameter has unknown entry provenance and contributes to
the existing unplaced-value count. It retains its width and consumption
facts. Managed parameters still enter with a transferred unit, and fresh
allocations in a function with raw parameters are still checked. No map
helper names are special-cased and no function is excluded from the walk.

On the same 519 clean fixtures, both versions walk 2,729 functions, skip
none, and encounter one existing lift failure. Findings change from four
values in two functions to zero. Unplaced values increase from 4,751 to
4,999: the additional 248 are consumed raw parameters whose entry units
were previously assumed. Poisoned roots remain 16,287. The census pin,
zero-finding requirement and coverage floors are unchanged.

This is an explicit limit of the static check, not proof that raw-pointer
cleanup cannot leak. Proving those entry units requires the erased value's
ownership contract or its relationship to the metadata that selects the
cleanup path. The runtime census remains a separate oracle.

Regressions compare identical conditional cleanup bodies taking `usize`
and `string`: the raw entry is unplaced, while the managed parameter's
unreleased branch is reported. Further cases preserve a fresh-allocation
leak beside a raw parameter and the parameter positions under single-word
and two-word string ABIs. Those targeted tests, the unchanged corpus gate
on macOS and Linux, the full unit suite, and `make lint-all` pass.
