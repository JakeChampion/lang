# Never map columns

Issue #12835 reports a typed-lowering refusal for a map payload in a bare generic enum.
Its omitted type argument settles at `never`.
The semantic type check refused that map even when the program used only the enum's concrete variants.

A never column now uses the narrow scalar map storage.
It can hold no entries of that type, so it needs no retained value or release helper.
Map type admission and snapshot admission use the same rule.
The physical map tag selector already uses the scalar tag for this type.
Unknown column types remain invalid.

The conformance case covers a never key, a never value, and both columns together.
Each branch checks the map length and its key and value snapshot lengths.
The semantic type test also checks admission, storage classification, and rejection of unknown types.
