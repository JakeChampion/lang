# OS byte API follow-up

Text APIs reject malformed UTF-8. Byte APIs preserve native POSIX names and link targets.
Coreutils uses explicit byte adapters for path resolution, link text, and directory traversal.
Directory traversal retains its open handles.

The measured source is `3048da4e51442f2baa9e8b045f5003329d72d214`.
The source inventory covers 4,389 files. The patch applies to public base `b49497ea8be5a072de645a510daaa1f7ced7f1be`.
The seal script checks the patch in reverse and records each artifact hash.

All six Go packages pass: 2,763 cases pass and three synthetic AST cases skip.
The OS, Reader, and byte-attribute suites pass 83 cases. Five Darwin cases skip on this Linux host.
The native and playground literate-library suites pass seven cases.
Go vet and Go format checks pass.

The CLI matrices pass 58 commands. They cover both interpreters, both Linux targets, WASI, and Darwin builds.
All 14 allocation counts balance, with zero live bytes.
The native tests preserve all 13 malformed UTF-8 byte classes.
WASI byte tests use valid names because its host interface can reject malformed names before Fern receives them.
No local Darwin execution is claimed.

The published seed release is `stage0-20261024-9eddfb5`.
Each host passes its bootstrap checks. Each downloaded binary matches the published lock and release digest.

The pin compiles the current source. All three local stages are identical at 14,454,144 bytes.
Their SHA-256 is `8782c18a6ec274c59c57eea95887ed2db492a1b1fde0b9054e81fd58e1920812`.
The temporary release workflow is absent from the final change.

All 11 strict driver size gates pass. The feature census and complexity limits also pass without baseline changes.
All compiler and library format checks pass, with literate sources included.

The earlier GNU logs retain their measured source, `705107d8fae4baff8c78e5304c11ec8861b1f387`.
They pass 1,673 cases on each Linux target against GNU coreutils 9.12.
The identity record proves that the native byte implementations and affected utility sources are unchanged since those runs.
The ARM runner executes Fern through QEMU and executes the GNU reference on the host.
Full pull-request CI repeats the current parity checks before merge.
