# Emit only the WASM buffer helpers a program uses

The primary WASM backend previously emitted the entire buffer runtime when
any capacity-carrying builder operation was reachable. A raw HTTP body
writer consequently included text transforms, text extraction and scalar
push helpers it never called.

Each builder operation now records its own runtime need. Emission retains
only those helpers, adding the shared reserve helper for push operations.
The same needs travel through unit emission and linking. Component validation
admits these guest-local operations without requiring host imports.

The HTTP byte-body fixture shrank from 61,279 to 60,113 bytes with this
change, a reduction of 1,166 bytes. The compiler, stdlib and fixture were
otherwise unchanged for this comparison. Runtime helper bodies and buffer
ownership contracts are unchanged.

Validation covers eleven builder cases under AST and production semantic
lowering. Each checks the emitted helper set and executes both a core module
and a Preview 2 component. Semantic core executions require balanced
allocation counts and zero live bytes. Components do not print a census;
their executions verify behavior. AST ownership limitations remain outside
this change.

A linked-unit regression splits text and raw-byte operations across units,
then checks the combined output. The existing two-unit and SSA unit tests
also pass. The published-seed Darwin bootstrap reaches an identical stage
1, 2 and 3 at 14,610,337 bytes, SHA-256
`5fc54e8bf22b93ee694176de47101f3e0a8036bea3681e4d626dc5f9c5d917b3`.

The final Darwin and Linux execution suites, full host unit suite and
`make lint-all` pass.
