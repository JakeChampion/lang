# A one-letter trait is not a type variable

The typed path refused `function f(x: dyn A + B + C)` as an uninstantiated
generic (#10323). A declaration is a template when a type variable appears in
its spelled types, and `semsource.spelled_typevar` counts any single capital
that names no struct or union as one. It scanned the trait names of a `dyn`
set like any other, so traits called `A`, `B` and `C` made `f` a template
nothing instantiated. The names a `dyn` set lists are traits, so the scan now
skips the set whole.

`TestSelfHostDynMultiTraitIR` moves to the CLI with this change; its
`three-trait-scalar` case is the one that found it.
