# Callback-result module-loader size measurement

The module-loader driver measures 11,387,536 bytes on PR #15233.
The previous baseline was 10,839,904 bytes.
The branch crosses the existing 5% tolerance.

[CI job 130346696451](https://github.com/JakeChampion/lang/actions/runs/42966994263/job/130346696451) supplies the exact measurement.
The archive retains the complete report for all 14 drivers.
The head and CI merge have the same tree.
`measurement.json` records both commits, the tree, measurements, and archive hashes.

The follow-up stores result flags beside the existing global function index.
This retains constant-time lookup and removes a separate name index.
The local stock build measures 11,387,376 bytes and passes its execution check.
The local measurement includes this metadata change.
The CI measurement precedes it.

The baseline changes only the module-loader driver row.
The strict gate and its 5% tolerance remain unchanged.
This archive does not establish a green complete CI verdict.
