# PIE segment assertions

The PIE smoke test still expects two load segments. A compact image with
constants has three: code, constants and writable globals. The old assertion
fails on both targets even though the relocation and output checks pass.

The helper now requires all three segments. It checks that the constant load
matches the protected range, includes zero-fill memory, and ends before
globals. It also checks that the virtual gap occupies no file bytes.
The static image, stack, relocation, output, ASLR, symbol and refusal controls
remain intact.

The ELF and PIE roots pass together in 51.203 seconds on Linux ARM64.
Both PIE targets and all refusal cases execute. Go vet and the format gate
pass. The compiler and standard-library inputs still match the Android gap
qualification. No compiler, cost, tolerance or baseline change.

The archived CI log retains the original segment-count failures.
The binding record identifies the final test source and compiler scope.
Every current-head non-Netlify check still gates merge.
