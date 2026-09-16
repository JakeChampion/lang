# 2026-09-16 — a polymorphic float settles at its binding

`var f = 2.5;` refused the whole of `var_inference` as "unresolved type
of binding f: f64": the checker types an unsuffixed float literal
polymorphic and settles it where it lands, and a binding with no
annotation is nowhere it lands, so the binding's type stayed polymorphic
and `semtypes.concrete` said no to it, spelling the f64 it was.

`bound_type` and `checked` now settle a polymorphic float at the width
the checker carried, which is the f64 an unsuffixed literal is; an
annotated `f32` binding was concrete already and is untouched. The RC
fixture `float_bound` runs three unannotated float bindings and a
comparison on all four targets; the print golden carries
`float_binding`.
