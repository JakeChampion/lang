# Control value fixture census rows

The ten new control value fixtures each have a measured census row.
Both the compiler built by stage0 and the final stage 3 compiler run each fixture.
All twenty runs pair every traced allocation with its release.
Each census row is zero, with no signal exits or unmeasured cases.
The array fixture emits allocation and release events in both runs.
The scalar fixtures can finish without heap allocations.

The report records compiler hashes, exact input hashes, commands and exit statuses.
All raw build and execution outputs are preserved with gzip.
The fixture bytes match the prepared grammar source.
The census metadata does not change compiler or library source.
