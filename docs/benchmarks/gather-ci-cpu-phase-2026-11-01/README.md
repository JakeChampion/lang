# Give the gather load gate a serial CPU phase

PR #13998's first full CI run, `41318514563`, fails
`TestSelfHostGatherConcurrency` in job `124884811835`. It receives 11,674
acknowledgements against the unchanged requirement of 12,800 from 512 peers
over five seconds. The passing main run receives 31,970 under the same test.
The failure and its dependent verdict failures remain recorded.
The owner then reruns the failed shard without source changes. It passes
with 31,820 acknowledgements. PR #13998 merges at `de2ec359` after all 96
current non-Netlify checks pass or legitimately skip. This permanent
scheduling repair preserves that retry and its evidence.

Both the main and PR fixed-point compilers emit byte-identical x86 server
assembly. The stock test compilers independently produce those same bytes.
Their SHA256 is
`1f7d988f5a3595b12601fea18b56892bc4f4d8190e512f3d7738f75905ff45db`.
The native ARM Linux server images are also identical, with SHA256
`9e052bb2b0a6ee08c6a09cb91dc7489d268696af83033644b04f1f73d3ac2485`.
This controls for the ownership change's effect on the tested server.
After integration with main `5abd69d3`, both fixed-point compilers again
emit identical x86 server assembly, now SHA256
`1328100531e6f1a30a6d19bc27c1e612bc5d78530d83968eab60c546c5655da1`.
The integrated raw compiler equals the qualified PR fixed point byte for byte.

## CPU controls

The original load script runs unchanged with Node 22.23.4 in an isolated
native ARM Linux container. A 32-peer pilot first checks the complete path;
the full run changes only the participant count to 512. Each load lasts five
seconds. Separate resource controls then change only the CPU quota.

| Peers | CPU quota | Acknowledgements | Result |
| ---: | ---: | ---: | --- |
| 32 | 2 | 3,008 | pass |
| 512 | 2 | 47,104 | pass |
| 512 | 1 | 41,472 | pass |
| 512 | 0.25 | 12,714 | original acknowledgement assertion fails |

The low-quota run reproduces the same assertion with the same server bytes.
These controls establish CPU sensitivity. They do not measure the CI host's
precise available CPU share or claim x86 timing parity. The raw records,
binary and script identities, logs, and compiler controls are bound in
[qualification.json](qualification.json).

## CI change

The load gate previously shared its runner with three other compiler test
workers. It now runs in the existing CLI job after those compiler workers
finish. Its compiler is already warm. No additional runner job is added.
The live-test selector owns it explicitly; the unused shard scheduling weight
is removed. Source gates check that every excluded test still has a runner.

The Go test and Node load script remain unchanged. All 512 participants,
the five-second load, snapshot rate, acknowledgement count and latency,
capacity rejection, and descriptor cleanup assertions remain strict.
No retry, quarantine, performance-baseline increase, or longer load deadline
is added. Native x86 CI on the final head remains the merge gate.

Local lint, actionlint, and all three test-selection guards pass. The source
audit passes 218 nodes; its apt-only case passes separately on Linux after
the recorded macOS skip. The source/runtime gate passes all five runtime
sets and 85 standalone drivers. These checks do not replace final native CI.
