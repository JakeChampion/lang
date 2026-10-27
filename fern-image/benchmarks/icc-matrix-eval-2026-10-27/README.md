# ICC matrix evidence

The feature source commit is `039bf0c92e747740c0808456543d2aa3ca268687`.
The three new result records contain the source, compiler, worker, bundle, Python, and runner hashes.
The source commit precedes every final campaign.

`campaign-summary.json` checks exact target sample equality and every worker census.
The maximum observed step distance is one, against the predeclared maximum of two.
The maximum absolute difference is also present and does not replace the step criterion.
This finite campaign does not establish a universal error bound.

`unit-source-link.json` retains the original precommit unit labels.
Its hash checks link both feature files to the committed source without relabeling those runs.
`committed-go.json` records the later clean-source Go check and interpreter execution.
`go-source-identity.json` checks the independent oracle implementation against the feature source.

`naive-control.json` records an ordinary sum that loses 74 representable steps on a cancellation vector.
The fifth test group requires that this naive sum fails the fixed criterion.
The compensated kernel must meet that same criterion.

The initial check failed on mistakes in the new test API and signed literal.
The matching check log, metadata, and test snapshot remain here.
A later runner-method correction used the existing `it` API.
These were development mistakes, not compiler defects or changes to assertions.

The full target logs report native and ARM sanitizer executions and Wasm leak censuses.
A standalone ARM sweep does not qualify the whole Go ARM harness.
The auxiliary record contains actual subcheck exit codes.
No CI state in this evidence converts a pending, skipped, cancelled, or failed job into a pass.
