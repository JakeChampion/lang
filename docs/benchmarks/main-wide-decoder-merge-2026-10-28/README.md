# Preserve the standalone wide decoder from main

PR #13718 now includes main at `8c688043465450e18520d7d32bfb0db8dde32ebf`.
That merge contains the standalone decoder from completed issue #13738 and PR #13743.
The local merge has no conflicts.
All eight selected decoder and legacy proof files retain their exact main-branch bytes.
The decoder does not complete the machine integration in #13717.

The combined branch passes 30 stock decoder, worker-protocol and import-free test nodes on three targets.
The recorded command returns zero, with zero failures and zero skips.
Source checks and unchanged feature and complexity gates pass.
The full formatter covers 2,100 inputs.
The qualified production compiler also reproduces itself byte for byte after the merge.

The `perf` directory retains exact artifacts from CI run `40624419024` at `fa0eedfa5`.
Both native performance jobs passed.
Checker allocations and frees each decrease from 34,278,531 to 33,753,130, a decrease of 525,401.
The baseline update preserves all eight changed measured rows and every tolerance.
All six raw-report comparisons pass against the updated baselines.
The other rows show mixed changes and do not establish another optimization gain.

These CI workloads use their own subjects and protocols.
They remain separate from the common-producer checker profile comparisons.
Raw artifacts, logs, hashes and the exact baseline patch remain available here.
The full current-head CI and #4412 acceptance criteria remain incomplete.
