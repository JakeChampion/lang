# A literal settles through an arm and a constructor

The self-host checker read `var x: i64 = if (c) { 5 } else { 9 };`,
`var o: Option[i64] = Some(40);` and `return Err(40);` from a function
returning `Result[i32, i64]` as i32-into-i64 assignments (#10343); native
accepts all three. `checker.settles_to`, the rule that reads an unsuffixed
literal at its destination, now reaches through a value-position `if` /
`match` (every arm settles) and through a builtin variant's payload.

The settle stays literal-shaped: `Some(n)` for an i32 `n` into an
`Option[i64]` is still E003, as it is on native. Native and the self-host
checker both admit `Ok(n)` for an i32 `n` into a `Result[i64, _]`; the
typed path refused that payload as the wrong width, and now widens it the
way it widens a binary operand.

`TestSelfHostWideLiteralSettle` pins both sides against native `-check`.
Four test files move to the CLI with this change.
