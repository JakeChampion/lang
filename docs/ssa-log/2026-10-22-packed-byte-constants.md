# Constant byte arrays stay packed

Issue #12857: an embedded byte array created one AST expression and one semantic
value per byte. A profile taken before this change placed most allocation in
semantic production, ownership plans, and RC lowering. The embed AST walk
accounted for 8.76% of the sampled allocation weight.

`ExprBytes` holds an embedded asset in one AST node. The semantic `bytes_new`
operation holds the byte count and canonical decimal payload, with no scalar
operands. Ordinary constant `u8[]` literals use the same operation. Expressions
with a nonconstant element keep their evaluation order through the original path.

The semantic verifier checks the payload, count, operand count, and owned `u8[]`
result type. The unit planner can share the static byte box. A literal
that reaches the raw memory floor gets a fresh writable copy. That copy carries
an allocation marker so the FIP verifier still charges its constructor.

The native and Wasm emitters read the byte payload without a string per element.
The wire format, byte stride, static headers, and copy-on-write behavior stay
valid on all three targets. A million-byte asset needs one semantic construction
and fewer than 32 physical IR operations. A separate test checks an ordinary
source literal with 10,001 elements.

Both interpreters and all three targets pass the functional tests. The tests
cover empty assets, all byte values, nested constants, aliases, mixed expressions,
raw writes, and invalid semantic input. Stages two and three match exactly. All
eleven driver sizes pass the strict gate with the recorded baselines.

The [measurement record](../benchmarks/packed-byte-constants-2026-10-22/README.md)
contains the profiles, commands, output hashes, and validation logs.
