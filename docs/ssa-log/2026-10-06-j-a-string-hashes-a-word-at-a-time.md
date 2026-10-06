# 2026-10-06 — a string hashes a word at a time

`__str_hash(s, seed)`, a kernel in ATLAS-PLATFORM-PLAN §3.3's family, on
every engine. Refs #8171. No emitted byte changes: the compiler before and
after builds `checker.fern` for x86-64, arm64 and wasm, and `fern.fern` for
x86-64, byte for byte.

## The shape

`util.hash_bucket` is 340 M Ir of the 17.6 G stage-2 profile over
`checker.fern`: 3.2 M calls, 2.5 M of them from `NameIndex.chain`, every
name lookup in the checker, the records, the ownership rows and the unit
planner hashing its name first. The loop is as tight as a byte loop gets,
six instructions a byte with the length and data pointer hoisted and no
bounds check, so the hash costs about a hundred instructions for a
fifteen-byte name. The map's string hash in `core/map` assembles four bytes
into a word from four indexed loads for the same reason: nothing in Fern
reads a word of a string.

## What changed

The kernel reads the string eight bytes at a time. Its definition, which
every engine implements and the tests pin to an independent computation:
FNV-1a's 64-bit basis xor the sign-extended seed xor the length; per 8-byte
little-endian word a xor and a multiply by FNV's 64-bit prime; the final
partial word is the last 8 bytes of the string, overlapping the word before,
or under 8 bytes the bytes zero-padded to a word; the high half folds into
the low and the low 32 bits are the answer. On x86-64 and arm64 the loop is
a load, a xor, a multiply, an add and a compare-and-branch per word; the
wasm helper is the same in i64; both interpreters assemble each word a byte
at a time.

`TestStrHashPins` holds the Go reference to eight values computed outside
the tree; the Go-side corpus (`internal/testing/e2e/str_hash_test.go`) runs
every length to 40 from six seeds, plus NUL, high and random bytes, on the
interpreter, x86-64, arm64 and wasm; the self-host program
(`self_host_str_hash_ir_test.go`) carries a Fern reference and the same
pins; the Fern interpreter's case is in the modload table.

## What is left

The compiler's own sources use the kernel once a published stage0 compiles
it: `hash_bucket` becomes `(__str_hash(s, 0) & 1073741823) % n`, which is
the measured half. `core/map`'s string hash and `std/string`'s `hash_fnv32`
are the same loop in the stdlib; moving the map onto the kernel moves the
`map_string` bench, so it is its own change with its own baseline row.
