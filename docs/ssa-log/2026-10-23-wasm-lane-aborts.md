# Wasm lane bounds and abort reporting

The lane-memory and abort-reporting changes merged independently.
Their combined tree called the bounds helper with its old argument count.
The compiler could not bootstrap from that tree.

Lane checks now pass the array cause to the helper.
The final-index check also reports that cause before it traps.
Lane loads and stores declare their abort need, including static array loads.
The component composer can therefore connect stderr when no allocation occurs.

Three new shared cases cover a negative first index, an invalid final load
index, and an invalid final store index.
All shared abort cases pass on x86-64, ARM64, and Wasm.
The source checks and feature census also pass.
