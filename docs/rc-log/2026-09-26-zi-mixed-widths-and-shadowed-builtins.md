# Mixed widths and shadowed builtins on the typed path

Moving another 68 behaviour test files to the CLI ran their programs through
the typed path, and three bugs in it surfaced:

- **A 32-bit operand beside a 64-bit one refused** (#10345). Native widens
  `i32` to `i64` and `u32` to `u64` for a binary operator and rejects every
  other mix with E009. `semsource.arithmetic` now casts the narrower operand
  up in exactly those two cases.
- **A wide literal beside an `i32` literal truncated** (#10345). In
  `1 < 4611686018427387904` the right literal took the left literal's `i32`
  width. A literal past the `i32` range now keeps its `i64`, and the
  widening above meets it.
- **A user function named like a builtin called the builtin** (#10344).
  `ssarc.call_site` chose a builtin's op by the call's name alone.
  `ssasem.Func.shadows` now lists the builtin names the module declares as
  free functions, and a call to one is an ordinary direct call. A method
  never shadows, as on the AST path (`is_user_fn`): std's `string.trim`
  and its siblings stand in for the runtime helpers, which still win.

Four more files stay on the drivers: the self-host checker types an
integer literal in a value `if` / `match` arm or a variant constructor as
`i32` where native takes the 64-bit destination (#10343).
