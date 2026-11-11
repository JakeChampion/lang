# Callback-result CI size measurement

The runtime driver measures 16,668,464 bytes on PR #15233.
The previous baseline was 15,871,648 bytes.
The branch crosses the existing 5% tolerance.

[CI job 130271740223](https://github.com/JakeChampion/lang/actions/runs/42943752293/job/130271740223) supplies the exact measurement.
The archived log includes the checkout identity and all 14 driver rows.
The head and CI merge have the same tree.
The local stock-driver gate measures the same 16,668,464 bytes and passes its execution check.
The paired explicit-stdlib builds measure 16,664,672 bytes on main and 16,668,464 bytes on the callback branch.
The callback change adds 3,792 bytes to the current-main driver.

`measurement.json` records source identities, measurements, and archive hashes.
The compressed log retains the complete original job output.
`local-driver.log` and `local-driver.tsv` retain the local stock-build result.
That measurement describes c9ce8161bd. Later compiler changes require their own qualification.

The baseline changes only the runtime-driver row.
The 5% tolerance and strict gate remain unchanged.
The existing `TestCheckDriverSizes` suite retains growth, shrink, missing-row, and incomplete-report refusal controls.
All 14 tests pass after the baseline update.

Other CI repairs reuse #15234 for the census row and #15236 for the spec index pins.
The OR-01 fixture explicitly shares the counter reference for its awaiting-token transfer.
This record does not establish a green complete CI verdict.
