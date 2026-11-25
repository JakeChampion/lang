# `@secret` record types

Status: [policy] normative for the native targets. Part of [SEC-025](https://github.com/JakeChampion/lang/issues/13527).

A struct with the attribute `@secret` holds secret data, such as a key.
When the last reference to a record of such a struct goes away, its drop zeroes each numeric array that the record holds alone.
The array then goes back to the allocator with zeros in its elements.

The drop also zeroes each scalar field of the record: an integer, a float, a boolean or a `char`.
The box of the record then goes back to the allocator with zeros in those fields.

The drop also zeroes the bytes of a string that the record holds alone, when the bytes are in the block of the string.
A string that the program builds at run time has its bytes in its own block.
Examples are a file that `read_file` reads, a concatenation, a `repeat` and a string that an append grows.
A string literal is read-only, and it keeps its bytes.

The drop does the same for a nested record and for a tuple that the record holds alone.
It zeroes the scalars of that record or tuple, and the numeric arrays that the record or tuple holds alone.
The rule applies again to a record or a tuple inside it.

The drop also does the same for an enum value that the record holds alone, such as an `Option`.
It zeroes the scalars of the variant, and the numeric arrays that the variant holds alone.
The drop zeroes a scalar payload of an `Option` or a `Result` of each width, also a 64-bit integer or a float.

The drop also does the same for each element of an array of boxes that the record holds alone.
Examples are a `u8[][]`, a `string[]` and an array of records.
An element that another value still holds keeps its contents.

The drop also does the same for a value of a `type A | B` union that the record holds alone.
The drop of a union value runs the finalizer of its member type first. Then it wipes the member.

The drop also wipes a record or an enum with a `core/mem.Drop` finalizer that the record holds alone.
This applies at each depth: a field, a field of a nested record, an element and a payload of an enum.
The finalizer runs first and reads the fields as they are. Then the drop wipes the fields and releases them.

This holds in each build, not only under `-harden`.

A numeric array has elements of 1, 2, 4 or 8 bytes: `u8[]`, `u16[]`, `i32[]`, `u64[]`, `f64[]` and the other integer and float element types.

An array or a string that another value still holds keeps its elements.
The drop wipes an array only when the record holds the last reference to it.
A nested record, tuple or enum value that another value still holds keeps its fields and its arrays.
A struct update `Key { ...k, id: 2 }` shares the arrays of `k`, so they are wiped when the last record that holds them goes away.

`@secret` applies only to a `struct` declaration. On any other declaration it is `P001`.

A type with the attribute keeps its box, as a type with a `core/mem.Drop` finalizer does, so the drop runs.
The attribute has no other effect on how the program runs. The two interpreters run a `@secret` record like any other record.

## Key types of the standard library

These records of `std/crypto` and `std/noise` have the attribute:

| Module | Records |
|---|---|
| `aes` | `Cipher` |
| `aes_gcm` | `Key` |
| `legacy/des` | `Cipher` |
| `ecdsa` | `PrivateKey` |
| `ed25519` | `PrivateKey` |
| `chacha20poly1305` | `Key` |
| `hpke` | `Context`, `HybridKey`, `HybridP256Key`, `P256Key`, `Encapsulation` |
| `mldsa` | `KeyPair` |
| `mlkem768` | `KeyPair`, `Encapsulation` |
| `noise` | `Setup`, `Handshake`, `CipherState`, `Transport` |
| `rsa` | `PrivateKey` |

A struct can have `@value` and `@secret` together. `aes_gcm.Key`, `ecdsa.PrivateKey` and `ed25519.PrivateKey` have both.

## Limits

- The drop does not wipe a string from `map_file_unchecked`.
  Its bytes are the pages of the file, mapped read-only, so the secret stays in the file.
- A copy of the elements into another array, such as a slice that a program copies out, is not wiped.
- The wasm allocator does not give a released block to a raw allocation of the same size.
  The read-back test therefore runs on the native targets only.

The semantic index is [spec/semantics.md](../spec/semantics.md).
`TestSelfHostSecretRecordWipesItsArrays` reads the released blocks back through raw allocations on x86-64 and arm64.
Its control is the same record without the attribute, which must read the secret bytes back.
`TestSelfHostSecretRecordWipesItsScalars` does the same for the scalar fields, `TestSelfHostSecretRecordWipesItsNestedBoxes` for a nested record and a tuple, and `TestSelfHostSecretRecordWipesItsEnums` for an enum value.
`TestSelfHostSecretRecordWipesItsStrings` does the same for a string, and `TestSelfHostSecretRecordWipesItsArraysOfBoxes` for the elements of a `u8[][]` and of an array of records.
`TestSelfHostSecretRecordWipesItsUnions` does the same for a union value, and `TestSelfHostSecretRecordWipesItsFinalizedFields` for a field with a finalizer.

`TestSelfHostSecretRecordWipesItsBuiltStrings` does the same for the strings that the runtime builds, and for a mapped file that the drop must leave.

`TestSelfHostSecretRecordWipesItsDeepFinalizedValues` does the same for a deeper value with a finalizer.
Its record holds such a value in a nested record, a tuple, an array, an enum and a recursive enum.

`TestSelfHostSecretRecordWipesItsRecursiveValues` does the same for each level of a recursive type: a list, a tree and a list of 100,000 nodes.
