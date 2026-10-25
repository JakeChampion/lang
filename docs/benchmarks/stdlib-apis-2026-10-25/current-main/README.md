# Current main validation

Main base: 6912c7718d176daf8996d55e75bf60f6d52c47fb.
The source and input certificate identify the exact validated tree.
The earlier final-validation capsule retains the full batch evidence.

Upstream adds image examples, formatter coverage, and escaped regex class coverage.
The formatter repair matches the batch's repair exactly.
The checked regex implementation keeps the escaped class behavior.
The new class tests use the checked compile and group text APIs.
Five JSON assertion helpers now call JsonObject.contains.

The latest main also changes the CI queue and removes duplicate interpreter sweeps.
The batch preserves those changes.
The regex TAP suite remains in the native stdlib lane.
The focused JSON, regex, and formatter checks pass.
The source checks and compiler budgets pass.

A fresh official bootstrap produces the same final compiler bytes.
Stage 2 and stage 3 match at 14,570,816 bytes.
The source certificate records the current compiler and library inputs.
The size report refreshes the two changed test drivers.
All 11 strict size gates pass.
The earlier runtime matrix uses the identical compiler.

The rebuilt embedded compiler passes 136 commands.
Its library inputs match the current library.
The image example script also passes.
All 1,235 Fern format checks pass.
The gofmt and vet checks pass.

The extended TAP sweep hit its ten-minute limit in slow digest examples.
Completed cases passed before the timeout.
That diagnostic run is not part of the pass totals.
The focused checks cover the changed JSON and regex callers.
