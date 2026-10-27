# Infer owned arrays beside empty match arms

A known owned array payload beside `[]` lost its element type in the value-block join.
Valid programs then failed self-host checks or typed lowering on all three targets.
The Go oracle accepted and ran the reduced case.

The join now adopts a sibling type only for actual empty-array trees that fit its array depth.
Material values and unrelated unknown expressions retain their existing paths.
Ownership/view distinctions and exact Go diagnostic controls remain unchanged.
The frozen ICC model application requires no annotation or syntax workaround.

The committed regression checks both arm orders, nested empty trees, tuples, imported/generic payloads, early returns, and COW ownership.
Before logs retain 18 typed-lowering refusals across six valid three-target cases.
The expanded targeted suite passes 66 nodes without skips.
Two later controls select the empty branch at runtime and pass nine follow-up nodes on all three targets.
Current-source broader checks pass 4,756 diagnostic nodes, 388 focused nodes, and 2,000 Go checker nodes without skips.

The clean two-stage compiler passes the stage1 smoke and unchanged frozen-application source check.
Full image scripts pass 268 TAP cases per target with balanced zero-live heap censuses.
The unchanged allocation gate measures 35,061,110 balanced allocations/frees, approximately 0.31 percent above its baseline.
Both unchanged complexity gates and format/build/vet/lint checks pass.

[Detailed evidence](../benchmarks/match-empty-array-2026-10-27/README.md) records sources, tools, errors, counts, and scope.
This fix changes no application syntax, Go checker implementation, baseline, tolerance, or exemption.
It does not establish application runtime qualification, physical/process bounds, or production acceptance.
