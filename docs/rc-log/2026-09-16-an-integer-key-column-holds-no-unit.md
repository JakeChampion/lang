# 2026-09-16 — an integer key column holds no unit

The semantic boundary admitted one map shape: a string key column over a
narrow scalar, string or string-array value column. The corpus's most
common refusal after the flip (#9437) was `unsupported map shape`, and the
sites behind it were `Map[i32, i32]` and `Map[i32, string]` far more often
than any other spelling.

## What changed

`ssasem.is_supported_map` admits a narrow integer key column beside the
string one. The runtime already had every piece: `__fern_map_new` and the
lookups take a key kind (0 a string column, 1 an integer one) on every
backend, and the free family has the members an integer column needs —
`__fern_map_free` frees both columns whole, `_vs` walks a string value
column beside it, `_vsa` a column of string arrays — the AST lowering has
called them all along.

The plan supplies a unit for the key of an insert only when the key is a
reference (`ssaunits.operation_supplies`): an integer key has none, and
the retain the plan used to supply would have counted a word that is not
a box. The physical lowering threads the key kind into `map_new`, the
insert, `has` and `get_or`, hands no key unit over for an integer column
(`kconsume` off, as the AST lowering's `is_fresh_str_temp` is for a
non-string key), and names the release helper by both columns
(`ssarc.map_release_name`).

`len` has a contract now too (`ssasem.map_len`): it borrows the map and
answers an i32, the same op every backend already emits for the AST
lowering.

## Pinned

`TestSelfHostSSAPhysicalRC` lowers an owned `Map[i32, i32]` dropped on
the way out to `__fern_map_free`, a `Map[i32, string]` to
`__fern_map_free_vs`, an insert with key kind 1 and the key-consume bit
off and no rc call in the body, and a `len` that reads the map and still
frees it. `TestSelfHostSemanticSourceRC` runs `map_ints` (a
`Map[i32, i32]` filled in a loop, overwritten, read back and measured) and
`map_int_words` (a `Map[i32, string]` overwriting a counted value) on
arm64, x86-64, the sanitiser and wasm under the leak check. The print
golden's `int_map_key` graph is the shape that used to be refused.

## Still refused

A key column of records or enums (the `Map[Name, i32]` shapes) needs the
derived hash and equality the AST lowering passes as key kind 2, and a
value column of records, scalar arrays or unions has no member of the free
family that walks it. `get` (an `Option` result the runtime boxes without
retaining a counted payload), `without`, the columns and the iteration
have backend ops and no contract yet.
