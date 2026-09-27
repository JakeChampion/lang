# A user's string method named like a runtime helper is called

`function (s: string) trim(n: i32): i32` with `"x".trim(21)` exits 42 on
native. On the typed path, `ssarc.string_method_site` lowered the call by
name to `op_str_trim`, which crashed on x86-64 and trapped on wasm (#10363).
The same happened at the helper's own arity, where the program ran but
returned the helper's answer instead of the user's.

Only std/string's own declarations of the seven helper names
(`to_ascii_upper`, `to_ascii_lower`, `trim`, `lines`, `split`, `repeat`,
`replace`) are equivalent to the helpers. Native rejects a second
declaration beside std/string's with E006. So a string method of one of
those names belongs to the user exactly when std/string is not in the
bundle's import closure (`parser.Module.imports` after flattening).
`semsource.shadowed_builtins` now lists such a method's key (`string.trim`)
in that case, and `ssarc.call_site` makes it an ordinary call as it already
does for a shadowed free function.

`TestSelfHostUserFnShadowsBuiltinIR` gains both arities.
`TestSelfHostSemanticProduction/string-helpers-take-the-typed-path` still
pins the helpers when std/string is loaded.
