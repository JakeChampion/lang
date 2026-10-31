# Printer corpus checks

CI run 41245971228 timed out after 25 minutes in TestFormatCorpusOutputTypeChecks on shard 1/2.
The shard selected 910 of 1821 files. The stack was in the Go checker.

The test checks each original source before it invokes the formatter.
When the formatted bytes match the original bytes, a second check repeats the same work.
The candidate omits that duplicate check and its file writes.
It retains every original check, the validity floor, and the second check for every changed source.
The test reports both unchanged and rewritten counts.

The formatter produced identical bytes for 1623 of 1821 files on the control tree.
The small 1/64 slice checks the same 17 valid programs before and after the change.
The candidate covers 16 unchanged programs and one rewritten program in that slice.

| Slice | Selected files | Valid programs | Observed seconds |
| --- | --- | --- | --- |
| Control 1/64 | 29 | 17 | 108.78 |
| Candidate 1/64 | 29 | 17 | 56.73 |

These single runs include concurrent local validation. They do not establish stable throughput.
Both full CI slices retain their 25-minute deadline and their complete file selection.
results.json records the failure and the small-slice measurements.
