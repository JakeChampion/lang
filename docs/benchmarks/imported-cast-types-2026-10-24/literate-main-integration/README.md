# Integration with the literate standard library

The measured source is `3cda652cef89a883f476afabadaaca85cd0d1f91`.
It merges main at `3cf4afe7dbb88d91ce8a189053136735506db5a5` into the compiler interface change.
The seven affected library modules retain every executable token from the earlier PR source.
The new literate format and prose remain intact.

All 2,495 selected Go cases pass across six packages without skips.
All 186 selected native cases pass without skips.
The suites test the resolved merge before removal of one blank line from a Map chunk.
The capsule retains that earlier Map input and the exact format diff.
Executable tokens remain identical.

The published stage0 pin produces identical stages 2 and 3 at 14,449,360 bytes.
Their SHA-256 is `b71a022817dc777f2639236e097241074deb59faa7a612e68dc5a1fb04bab4c9`.
These bytes also match the compiler from the earlier PR source.
All 11 strict driver size gates pass again without baseline changes.
The manifest records each driver image separately.

The CLI matrices pass 177 commands across both interpreters and four target builds.
All 51 runtime allocation counts balance with zero live bytes.
Matched direct loops retain 60,000 allocations for 10,000 loops before and after the Map change.
The owned iterator path retains 420,000 allocations for that loop count.
Darwin images are built but are not executed locally.

The audit reports zero missing paths in current documents.
Sixteen current citations now point to the correct source files.
The 599 historical or proposed references retain their declared status.
Go vet, Go format, and compiler and library format checks pass.

The capsule records 498 compiler and library files and 360 inputs and output logs.
The source patch excludes benchmark evidence files.
Earlier capsules retain their measured source scopes.
