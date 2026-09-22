# A keyed column is released through its own function

2026-09-22. #9962. The typed path admits a struct- or enum-keyed map, and the
key column it owns is walked through the key type's release.

## What it was

`ssasem.is_supported_map` admitted a string or narrow integer key and nothing
else, so every `Map[Coord, V]` refused `unsupported map shape` and took its
whole module to the AST lowering — 201 declarations across three corpus
files, the largest remaining typed-path leaf. The admitting half was cheap
and known (#9962's patch); what held it back was that a struct key column had
no release. `ssarc.map_release_name` chose a member of the map free family
from the VALUE column and whether the key was a string, so a keyed column fell
to a member that frees the column's buffer and strands every key box.

## The shape

A keyed column is a column of boxes exactly as a boxed VALUE column is, and
the value side already had the answer: the `_vf` members take the value's
release as a further argument and call it on every entry, so the runtime walks
a column whose shape it never learns. This applies the same trick to the key
side. Key kind 2 takes a `_kf` member — `_kf`, `_kfvs`, `_kfvsa`, and `_kfvf`,
which takes both releases, the key's under the value's — and `ssarc.release`
pushes `__sem_release_<K>` after the box, then `__sem_release_<V>` when the
value column is boxes too. On the register backends the four are one
generalised emitter that walks either column through a caller-passed release;
the `_vf` pair is now emitted by the same function with the key walk off.
On wasm `map_release_func` grew a key mode beside its value mode.

The same release reaches `op_map_set`: an overwrite keeps the existing key
and discards the consumed one it was handed, and the register runtimes freed
it through `__fern_str_free` whatever its kind. `op_map_set.str` now carries
three symbols, `eq|vrel|krel`, split by `ir.map_set_symbols`; the AST path's
bare `K.eq` and the typed path's bare value release both still read as the
one symbol the op's kinds call for. x86-64 loads the key release into %r11
and arm64 into x7, and the runtime picks it for key kind 2 under kconsume,
the shallow dec when none was passed, and the string free otherwise. wasm's
`$__fern_map_set` takes `kdeep` and `kf` the way it takes `vdeep` and `vf`.

The probes carry `K.eq` and the construction `K.hash|K.eq`, spelled as the
AST lowering spells them, so the derived methods `@derive(cmp.Eq, cmp.Hash)`
synthesises are the ones every backend dispatches through. A column snapshot
admits a column of boxes on either side — the runtime's flag-2 snapshot is a
per-element `rc_inc`, which retains a struct box as it retains a string — so
`keys()`, `values()` and `for (k, v) in m` all produce over a keyed map.

## Measured

`FERN_SANITIZE=1 FERN_LEAKCHECK=1`, x86-64, a program over `Map[Name, i32]`
with a string field in the key, `Map[Tag, i32]` with a string payload in an
enum key, and `Map[Coord, Box]` shared and written through:

| leg | allocs | frees | live_bytes |
| --- | ---: | ---: | ---: |
| typed, this change | 103 | 103 | **0** |
| AST | refused: `field access .rank` on an iterated key | | |

The absolute gate is `TestSelfHostMapKeyedTyped{X86_64,Arm64,Wasm}`: three
programs, each compiled through the typed path with the tally checked whole,
over 1000 build-and-drop rounds with the heap bump read either side and
`__rc_underflow_count()` read after — a struct key with a string field
overwritten through a fresh value-equal key, an enum key with a string
payload over a column of boxes grown from capacity 2, and a shared map
rebuilt on write with the key also held by a local. All nine legs read a flat
heap and zero underflow.

Nothing else moves: `lexer.fern` and `ssarc.fern` compiled by this compiler
and by one built from the branch's base emit byte-identical modules on all
three targets, which is what the bare `str` reading of `op_map_set` buys —
a lone value release or a lone `K.eq` is spelled as it was.

Corpus census over `conformance/cases`, x86-64, this compiler against one
built from the branch's base:

| | before | after |
| --- | ---: | ---: |
| cases produced whole | 508 of 597 | **509 of 597** |
| declarations produced | 17,902 of 18,452 | **17,972 of 18,452** |

Exactly three files move and nothing regresses. `map_iter_struct_value`
goes from 0 of 70 to 70 of 70 and answers its expected 42, which is the +70.
The other two struct-key files now report exactly one refusal each, `delete
orphans a counted column`, which is #9970's — the delete that releases what
it removes is the next PR, not this one.

## Found on the way

`treeshake` rooted a `@derive`d method by the receiver it found in
`mod.structs`, and an enum's derives ride its VARIANTS' declarations while the
methods take the enum as receiver — so `Tag.hash` on `@derive(cmp.Hash) enum
Tag` was shaken away whenever nothing in the program spelled a `.hash()`. The
register backends never call the hash, so only wasm noticed, and only on a
program small enough not to reach core/map's own `.hash()` calls: `unknown
func: failed to find name $Tag.hash`, on the AST leg as much as the typed one.
A variant now roots the methods of its owner, and
`TestSelfHostTreeshakeKeepsDerivedMethodsX86_64` carries an enum beside its
struct.

`TestSelfHostMapBoxKeyReclaim{X86_64,Arm64}` was written for the AST path's
`MAPKA:` credit (#9966), and with the typed path the default and now
admitting its programs, an unqualified run would have measured only the typed
path and left the credit untested. It runs both legs by name now.

## What it does not reach

A keyed map's `without` still refuses (#9970): the runtime's delete releases
nothing it removes, on any backend, and that is what keeps
`map_struct_enum_keys` and `map_struct_key_grow` on the AST lowering. A
generic instance as a key (`Pair[i32]`) is refused with the other generic
nominals here; the derived methods are not named by the arguments this
boundary would have to spell.
