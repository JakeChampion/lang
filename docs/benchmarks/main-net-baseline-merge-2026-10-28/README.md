# Main address-parser and native baseline merge

The branch merges main at `a26e7c62054053a9c4e4902c80b78d632d2f74ff` after the measured view-map change `a47651398e8aa03805e4844e09eb2a4f5054df22`.
Main changes 27 files with typed address-parser errors and native baseline updates.
The merge retains exact main bytes for 26 files.
The perf baseline retains the branch comments and adds a dated resolution note.
Every metric row matches main, and every tolerance remains unchanged.

Two native `map_string.ir` rows conflict with earlier branch measurements.
The resolution retains the owner values from commit `1c8d476b925fedf8be083c92fb10215ce0ee8c33` in PR #13770.
That commit cites perf run [40649279321](https://github.com/JakeChampion/lang/actions/runs/40649279321) and its reports for both architectures.
The x86-64 row uses 58,500,783 instructions, and the ARM64 row uses 59,261,619 instructions.
Both rows retain their 2% tolerance.
The resolution proof verifies all 81 updated rows against main and preserves every tolerance.

All 1,072 compiler files remain identical to the measured view-map source.
The prior stage-2 binary, common producer and frozen benchmark subject remain applicable.
The integration requires no duplicate compiler build or profile.
It does not establish a new speed improvement or bootstrap fixpoint.

The selected address-parser, DNS and forwarded-header suites pass 11 nodes in 181.695 seconds.
There are zero failures and zero skips.
Source checks pass in 11.574 seconds.
Feature and unchanged complexity checks pass four nodes in 9.121 seconds.
All 2,110 formatter inputs pass in 3.704 seconds.

The archives retain all three conflict stages, the unresolved file, the resolved file, source identities and validation records.
Every compressed file passes a decompression identity check.
Applicable current-head CI, retained-machine integration and issue #4412 remain incomplete.
