# An array of a parameter's views takes the typed path

#10215: a function building a `str[]` from slices of its parameter was refused
("view element escapes its source"), and its callers with it. The whole module
then fell back to the AST lowering, which ran correctly but leaked each view's
24-byte box: `allocs=6 frees=3 live_bytes=72` on the issue's program.

A single `str` result was already anchored to the parameter it reads. An array
of views now gets the same anchor. The typed path already had the mechanism, it
just stopped at `.append` and `.with`:

- `ssasem.gathers_views` counts `append` and `with` beside the constructions,
  so inside a body the array is anchored to the one value its views read
  (`bytes_root`), and that source stays alive while the array does.
- `ssasem.view_sources` follows them too, so a returned array anchors to its
  parameter and the caller keeps that argument alive while the result lives.
- semsource no longer refuses a view element in an array literal, `.append` or
  `.with`.

The array's drop already released a `str` element through
`__fern_str_view_free`, which frees a view's box, releases a counted string and
skips a static literal. The issue's program now balances at 6 allocs and 6
frees.

What stays refused, each for a real reason:

- views of two parameters in one array ("a value holds views of two
  sources");
- views of a string made inside a loop, collected in an array that outlives the
  iteration ("dependency unavailable at use");
- a borrowed `str` parameter appended to an array ("a view is lent, never
  retained");
- a view as a cell's element or a map key, which nothing anchors.

## Tests

- `TestSelfHostSemanticProduction` row
  `an-array-of-a-parameters-views-is-anchored-to-it`: append, a loop, and a
  literal rebuilt by `.with`, all views of one parameter, no leak, on x86-64
  (with the sanitizer too), arm64 and wasm.
- `TestSelfHostSemIRStrict`'s refused module is now the two-parameter array.
- `TestSelfHostSemanticSourcePrint`'s `view_element` fixture is produced, and
  the golden prints its graph.
