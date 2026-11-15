# `@secret` record types

Status: [policy] normative for the native targets. Part of [SEC-025](https://github.com/JakeChampion/lang/issues/13527).

A struct with the attribute `@secret` holds secret data, such as a key.
When the last reference to a record of such a struct goes away, its drop zeroes each numeric array that the record holds alone.
The array then goes back to the allocator with zeros in its elements.
This holds in each build, not only under `-harden`.

A numeric array has elements of 1, 2, 4 or 8 bytes: `u8[]`, `u16[]`, `i32[]`, `u64[]`, `f64[]` and the other integer and float element types.

An array that another value still holds keeps its elements.
The drop wipes an array only when the record holds the last reference to it.
A struct update `Key { ...k, id: 2 }` shares the arrays of `k`, so they are wiped when the last record that holds them goes away.

`@secret` applies only to a `struct` declaration. On any other declaration it is `P001`.

A type with the attribute keeps its box, as a type with a `core/mem.Drop` finalizer does, so the drop runs.
The attribute has no other effect on how the program runs. The two interpreters run a `@secret` record like any other record.

## Limits

- The drop wipes the numeric arrays that are fields of the record. It does not wipe a scalar field, a string, or an array inside a nested record or an enum.
- A copy of the elements into another array, such as a slice that a program copies out, is not wiped.
- The wasm allocator does not give a released block to a raw allocation of the same size.
  The read-back test therefore runs on the native targets only.

The semantic index is [spec/semantics.md](../spec/semantics.md).
`TestSelfHostSecretRecordWipesItsArrays` reads the released blocks back through raw allocations on x86-64 and arm64.
Its control is the same record without the attribute, which must read the secret bytes back.
