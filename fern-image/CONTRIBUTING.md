# Contribute

Run `scripts/test.sh` with the audited compiler before you submit a change.
Format Fern sources with `fern -fmt -w .` from this package directory.
Add bounds tests for every public operation that reads untrusted values.

Keep core code in Fern.
Document compiler evidence before you depend on a new capability.
Do not add external codecs without explicit approval.

Keep HTTP and storage outside the pixel package.
Record performance claims with reproducible measurements.
