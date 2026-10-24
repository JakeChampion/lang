# Contextual integer literals and test consumers

The compiler source is `805b03ab74660e5a511d86d69131b050a75904a4`.
The final test source is `059eb35055cde2b94918b4e8ba68b0b2580b06fd`.
The compiler and library files are identical at these commits.

An integer literal expression must retain its destination context.
The pass that widens typed values inserted a cast inside a hexadecimal expression before its outer `u32` context was applied.
That cast kept a wider operation in compiled output, although both interpreters gave the correct result.
Literal trees now remain available for contextual type resolution.
Typed value operands retain automatic lossless conversions.

The hexadecimal fixture passes through six eligible fixture runners.
It checks 14 integer behaviors, with normal and allocation-quarantine execution.
All 163 focused native numeric cases and 1,869 Go checker cases pass without skips.
All 1,023 numeric property cases pass.
The local kernel lacks the TLS upper-layer protocol, so its separate kernel runtime test skips.
Both affected generated consumer programs pass checks and native compilation.

Nine test source files receive only the bare byte reader type rename to `ByteStream`.
The certificate verifies that generic `Stream[T]` names remain intact.
The E061 expectations now contain the primary error without the retired second error.
The mixed signedness test accepts the lossless i64 sum and rejects an implicit return conversion to i32.
All 42 updated reader and diagnostic cases pass.
All 32 native reader cases pass.

The fresh bootstrap reaches identical stages 2 and 3 at 14,449,360 bytes.
Their SHA-256 is `5e515d0038b8203a8f2a1457711e345886fa036ae490481b58f30873ec2c13a2`.
All 11 strict driver size gates pass without baseline changes.
Fresh matrices pass 50 commands with 12 balanced allocation counts and zero live bytes.
They cover both interpreters, three execution targets, and four Darwin builds.
Darwin images are not executed locally.

All 78 static benchmark rows across both Linux architectures remain within tolerance.
The six Map baseline changes retain the measurements recorded in the prior capsule.
The complete earlier x86 CI report also passes the updated strict baseline.
That CI report retains its earlier compiler source scope.
Format checks, Go vet, the feature census, and the complexity limit pass.
The capsule records source files, command output, source scopes, hashes, and the exact change patch.
