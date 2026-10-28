The stock GNU-assembly harness rejected duplicate textual labels in the first pool candidate.
The in-process assembler used distinct label IDs and completed all six direct cases.
Those direct passes do not qualify the GNU path or the later correction.

The complete targeted run finished with 16 passing nodes, 14 failing nodes, and zero skips.
The runtime checks, source checks, complexity gate, and 326-test ARM image run completed on that candidate.
The two active native and Wasm image runs stopped with actual outer exit 143 after the defect became clear.
Their partial logs remain unqualified.

The correction gives each pool boundary a name derived from its unique position within the function.
All regression sources, expected values, array sizes, validation flags, and parent arithmetic files remain unchanged.
A new clean build and new source-pinned qualification will cover the corrected source.
The earlier source, build identities, logs, results, and stop metadata remain separate.
