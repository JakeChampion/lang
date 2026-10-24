# Documentation source paths

Status: [record] verification for issue #12735.
Source paths and results in this record describe the measured commit.

Implementation: `442df72470491be0614ca8ff2f6378547cf3dfe0`.
Base: `8547531b2a3464b0e086527d76668a47e468f948`.

The current documentation has no missing repository paths under the new
source-path gate. The gate covers declared policy, reference and tracker
pages, the specification, diagnostic explanations and the main readmes.
It checks at least 100 current documents. It checks every alternative in
brace patterns and accepts file globs, relative references and source positions.
Fenced examples and unqualified Fern example file names are outside that check.

The audit retains 560 historical or proposed references across 92 documents.
Each document explicitly identifies that context. The historical text stays
in place. The old standard-library implementation strategy moves into an
archival quotation. Its text matches the original section at the base commit.

The full source-lint suite passes. It covers the source-path gate, its parser
cases, the documentation index and test selection. Diagnostic tests and vet
pass. New prose and comments pass the STE structural check.

Fresh Go and self-hosted CLIs print identical E044 explanations. Their bodies
match the canonical explanation and point to `compiler/lift.fern`.
The self-hosted CLI builds from the locked stage-zero compiler.
The compiler and standard library sources are unchanged from the base.
The workflow and production Go file edits change comments only.

`manifest.json` records each proof file's length and SHA-256.
`source.patch.gz` applies to the base commit.
`audit.py` uses `/workspace/lang-doc-paths-current` as its source root.
The audit's unresolved entries belong to explicit historical or proposal
contexts. A current missing reference fails `TestLivingDocPathsExist`.
