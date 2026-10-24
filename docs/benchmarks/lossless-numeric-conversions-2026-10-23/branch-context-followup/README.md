# Branch conversions and diagnostic recovery

Both checkers apply a destination type to fresh enum constructors in either branch order.
Stored containers retain their current element types.

The Go checker retains numeric conversions after an earlier branch fixes a literal width.
It checks all branch result types after type resolution.
A stored container cannot acquire a different element representation through another branch.

Invalid array literals retain their primary error when the destination can accept the first element.
Other first-element type errors retain their diagnostics.

The native checker reports E061 for a missing block value without a second E003 error.
The semantic lowerer preserves a refusal before it reads a result value.
The effect checker treats built-in string and array length operations as built-ins.
This prevents calls to unrelated methods in its effect graph.
Programs without platform functions skip the extra type annotation pass.

Parser tests expect refusal for loops without braces.
Map bounds tests use owned entry snapshots.
Numeric tests accept lossless conversions and retain cases that require refusal.
The Map runtime test checks the full value 437 and returns a portable status.
The semantic print golden changes only three generated lambda identifiers.

Each validation record identifies its exact source scope and compiler hash.
The source certificate identifies unchanged Go checker, parser, formatter, and library inputs.
Earlier failed runs remain available with their failure or timeout status.

The final source is `7f1614931b0da0849f93fd55046d82c31db77ed8`.
The native equality checker accepts scalar operands with a common lossless integer type.
A new fixture checks function results, local values, both operand orders, and mixed signedness.

All 4,858 final native regression cases pass without skips.
All 51 final fixture and diagnostic cases pass without skips.
The earlier broad run passes 3,002 cases and records 19 WASI capability exclusions.
Its checker mutation run passes all 400 seeds.
The Go checker, parser, and formatter run passes 2,290 cases across three packages.
Its inputs match the final source, as the source certificate verifies.

The final bootstrap reaches identical stages 2 and 3 at 14,449,408 bytes.
Their SHA-256 is `c7c5b910f31a3b89cabd5d8947102506df4d5c3d17e25442a10bafcbc1fbaf6a`.
All 11 strict driver size gates pass.
All 1,205 source format checks pass.
Go format and Go vet checks pass at the recorded identical Go source.
Darwin builds are checked locally but are not executed here.
