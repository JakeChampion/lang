# A downcast has a type

`var o: Option[Circle] = s as? Circle;` failed to compile on the self-host
compiler (#10347): its checker gave `x as? T` no type, so a downcast
worked only as a `match` scrutinee, which infers nothing from its operand.
Two type readers needed the arm. `checker.check_expr` now types `as?_T` as
`Option[T]`, and so does `asmcore.infer_expr_unary_type`, the pre-codegen
type gate, which read it as `i32` and reported E003 once the checker
passed it.

`TestSelfHostDynTraitIR` gains a `downcast-binding` case.
