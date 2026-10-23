# Ownership contracts for indirect calls

An indirect call reads its consuming positions from the function type.
This applies to parameters, local closures, and function fields. The check
does not depend on an unrelated owned function in the module.

The Go checker runs its ownership guard when a call has a consuming mask.
It also applies borrowed positions and local argument deaths to function
fields. A borrowed callback can read an owned argument more than once.

The self-host reads the same masks from callable types. Its existing death
analysis collects candidates for indirect arguments. The ownership guard
admits those candidates only at consuming positions.

Twelve cases compare codes, full messages, and positions across both checkers.
They cover E051, use after a move, last-use locals, fresh values, scalar
positions, and repeated borrows. The conformance case transfers allocated
strings through all three callable forms.

This resolves #12847.
