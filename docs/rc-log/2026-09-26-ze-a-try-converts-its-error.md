# A `?` converts its error on the typed path

The typed path refused a `?` whose failure payload differs from the body's
("try failure does not match the body's result", one of the shapes in
#10323). Two conversions are legal there, and the AST lowering made both:

- the body's error type is `dyn Trait`: the payload widens, the way any value
  reaching a dyn destination does (`dyn_up`, or `dyn_box` for a primitive);
- otherwise the body's error type supplies `from(E1)`: the failure returns
  `Err(E2.from(e))`, native's `tryConvertErrViaFrom` desugar.

`semsource.try_expr` now applies whichever holds, and still refuses when
neither does.

The second needed a tree-shake fix. The self-host tree-shake keeps what the
syntax names, and a `?` names nothing, so `E2.from` was pruned from every
program that reached it only through `?`. The AST lowering would have called
a symbol that was no longer there. A `?` now roots `*.from`. Native's
tree-shake runs after the checker has spelled the call, so it never had the
gap.

`TestSelfHostErrorTraitIR` and `TestSelfHostTryFromIR` move to the CLI with
this change, and both now require a balanced leak census.
