# `@secret` record types

Status: [policy] normative for the native targets. Part of [SEC-025](https://github.com/JakeChampion/lang/issues/13527).

A struct with the attribute `@secret` holds secret data, such as a key.
When the last reference to a record of such a struct goes away, its drop zeroes each numeric array that the record holds alone.
The array then goes back to the allocator with zeros in its elements.

The drop also zeroes each scalar field of the record: an integer, a float, a boolean or a `char`.
The box of the record then goes back to the allocator with zeros in those fields.

The drop does the same for a nested record and for a tuple that the record holds alone.
It zeroes the scalars of that record or tuple, and the numeric arrays that the record or tuple holds alone.
The rule applies again to a record or a tuple inside it.

The drop also does the same for an enum value that the record holds alone, such as an `Option`.
It zeroes the scalars of the variant, and the numeric arrays that the variant holds alone.

This holds in each build, not only under `-harden`.

A numeric array has elements of 1, 2, 4 or 8 bytes: `u8[]`, `u16[]`, `i32[]`, `u64[]`, `f64[]` and the other integer and float element types.

An array that another value still holds keeps its elements.
The drop wipes an array only when the record holds the last reference to it.
A nested record, tuple or enum value that another value still holds keeps its fields and its arrays.
A struct update `Key { ...k, id: 2 }` shares the arrays of `k`, so they are wiped when the last record that holds them goes away.

`@secret` applies only to a `struct` declaration. On any other declaration it is `P001`.

A type with the attribute keeps its box, as a type with a `core/mem.Drop` finalizer does, so the drop runs.
The attribute has no other effect on how the program runs. The two interpreters run a `@secret` record like any other record.

## Key types of the standard library

These records of `std/crypto` have the attribute:

| Module | Records |
|---|---|
| `aes` | `Cipher` |
| `aes_gcm` | `Key` |
| `legacy/des` | `Cipher` |
| `ecdsa` | `PrivateKey` |
| `ed25519` | `PrivateKey` |
| `chacha20poly1305` | `Key` |
| `hpke` | `Context`, `HybridKey`, `HybridP256Key`, `P256Key`, `Encapsulation` |
| `mlkem768` | `KeyPair`, `Encapsulation` |
| `rsa` | `PrivateKey` |

A struct can have `@value` and `@secret` together. `aes_gcm.Key`, `ecdsa.PrivateKey` and `ed25519.PrivateKey` have both.

## Limits

- The drop does not wipe a string. It does not wipe an array or a scalar inside an array of records.
- The drop does not wipe a 64-bit integer or a float that is the payload of an `Option` or of a `Result`.
  The payload store of that layout takes a 32-bit value.
- The drop does not wipe a value of a `type A | B` union.
- The drop does not wipe a nested record or enum that has a `core/mem.Drop` finalizer, because the finalizer reads the fields after the wipe.
  Give a record of that kind the attribute `@secret`, and its own drop then wipes it after the finalizer.
- A copy of the elements into another array, such as a slice that a program copies out, is not wiped.
- The wasm allocator does not give a released block to a raw allocation of the same size.
  The read-back test therefore runs on the native targets only.

The semantic index is [spec/semantics.md](../spec/semantics.md).
`TestSelfHostSecretRecordWipesItsArrays` reads the released blocks back through raw allocations on x86-64 and arm64.
Its control is the same record without the attribute, which must read the secret bytes back.
`TestSelfHostSecretRecordWipesItsScalars` does the same for the scalar fields, `TestSelfHostSecretRecordWipesItsNestedBoxes` for a nested record and a tuple, and `TestSelfHostSecretRecordWipesItsEnums` for an enum value.
