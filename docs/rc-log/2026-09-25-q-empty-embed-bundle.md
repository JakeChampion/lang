# An empty embed bundle takes the typed path

Under `FERN_SEM_IR_STRICT`, `TestSelfHostEmbedMatchesNative/assets-empty-bundle`
was refused: "unresolved array literal type". The embed pass expands
`__fern_assets()` into an array literal of `(name, contents)` pairs and stamps
the literal's `elem_ty` as `(string, string)`, so that an empty bundle still
names its element type. The checker typed every empty literal
`unknown[]` without reading the stamp. With no destination type in
`for a in __fern_assets()`, semsource had nothing to lower the literal to, and
the module fell back to the AST lowering.

The checker now types an empty literal that carries `elem_ty` as an array of
that type (`Scope.resolve_type`). Only the embed pass stamps a literal the
checker sees; irlower's stamps are made after checking.

`TestSelfHostEmbedMatchesNative` builds its self-host leg under
`FERN_SEM_IR_STRICT=1`, so every bundle shape has to take the typed path.
Without the checker change it fails on `assets-empty-bundle` alone.
