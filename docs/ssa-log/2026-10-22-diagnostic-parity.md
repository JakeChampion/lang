# Diagnostic parity for the error corpus

The self-host checker now matches every compile-error case in the conformance
corpus. The known-divergence list is empty. This resolves #12889.

Arithmetic errors name the required numeric type. Capture errors include the
known capture type and the reference-cycle reason. Collection-result errors
include the explicit-discard advice. Loop errors include the label. Nesting
errors include the parser limit. Match and if expressions use distinct wording.

The compiler supplies the entry filename to the method table. Ambiguous method
errors sort their providers and retain declaration locations in every CLI mode.

A value-less block has type void. The semantic checker reports E061 and the
destination mismatch. The parser accepts the block syntax. Refutable binding
patterns report one precise P001 and recover past the rejected pattern.

Tuple matches check each arm against the actual tuple size. Invalid arms do
not bind names or cover values. The checker retains their source bodies for
the undefined-name diagnostics that the Go checker reports. The existing
pretype scan also finds tuple matches, so this needs no separate AST scan.

Explicit nominal arguments with different counts do not assign. A malformed
generic instance retains its declaration variables in field diagnostics.

Validation includes the complete error corpus and text comparisons across all
CLI modes. Parser checks use the formatter and language server. The complete
conformance leak census also runs.
