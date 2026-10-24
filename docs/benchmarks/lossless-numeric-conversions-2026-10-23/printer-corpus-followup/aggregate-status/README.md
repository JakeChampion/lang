# Aggregate unit status checks

The original unit status names remain available for both hosts.
Each aggregate check requires every unit worker to succeed.
The status checks cover all corpus workers and the other unit packages.

Source lint passes all 199 checks without skips.
Actionlint passes with no output.
The compiler, library, frontend, and printer inputs retain their bytes from the four-worker measurement.
The source record identifies the exact compared paths.
