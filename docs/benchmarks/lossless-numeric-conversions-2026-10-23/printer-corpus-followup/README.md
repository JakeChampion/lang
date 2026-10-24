# Formatter corpus checks

Source: `05d1428d03ff959c91f356d188d8f938652fcfb3`.
The ownership change is `e3cff7a18658fcce0c9e12b307d962d2c5c1354b`.
The compiler, library, stage0 pin, and size limits retain their exact bytes.
The source certificate identifies their earlier native measurements.

The earlier CI run passed every other job and hit the 25-minute unit-test limit.
The failed log and a CPU sample identify the measured workload.
The sample is an observation, not a complete test run.
Two local unpartitioned iteration runs were stopped before completion.
They are not recorded as successful checks.

Ownership analysis counts field selections once per function when needed.
The transfer rules use the same identifier and field counts.
Seven cases check repeated fields, whole owners, captures, and shadowed names.
The benchmark checks the same transfer results at three field counts.
Its raw times are observations and do not form a test limit.

Four workers partition the sorted corpus on each host.
Each file belongs to exactly one worker.
The workflow denominator comes from the matrix.
A source gate verifies all worker indices on both hosts.
Each worker must check at least 75 valid formatted programs.
The total coverage floor remains 300 and the timeout remains 25 minutes.

The local corpus has 1,032 files, with 258 assigned to each worker.
All four worker suites pass without skips.
The unit checks cover configuration, disjoint coverage, and the workflow matrix.
The CLI matrix checks the current Go oracle against the unchanged native image.
