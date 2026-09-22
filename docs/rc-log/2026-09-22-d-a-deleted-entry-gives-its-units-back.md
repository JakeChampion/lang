# A deleted entry gives its units back

2026-09-22. #9970. `m.without(k)` releases the removed entry's key and value,
on every backend, and the typed path admits a delete over a counted column.

## What it was

The runtime's delete removed an entry and released nothing it held. The
register backends' `__fern_map_delete` swapped the last entry into the hole
and shrank both columns, so the removed key and value were overwritten in
place and the map free's length-bounded walk never reached them; wasm's
tombstoned the slot, and `$__fern_map_release` visits live slots only. Every
counted box in a deleted slot was stranded for the life of the program.

`semsource.map_delete` refused rather than describe a removal it could not
account for, which was right — and it was the ONLY refusal left standing on
`map_struct_enum_keys` (68 declarations) and `map_struct_key_grow` (63) once
#9962's keyed column landed, and it kept every program deleting from a
string-keyed or string-valued map on the AST lowering.

## Where the release goes

Not inside `__fern_map_delete`. It is a Fern runtime source lowered through
the IR, shared by both register backends, and the AST lowering calls it too:
an AST map owns its columns only under a credit the delete does not read, so
a release there would over-release every uncredited AST map. The AST path
keeps the release-nothing delete, byte for byte.

The typed path's maps own one unit of every entry, so its delete takes
`__fern_map_delete_rel(mapbox, key, keykind, eqfn, kfree, vkind, krel,
vrel)`: `__fern_map_delete` widened to hand back the removed key and value at
words 2 and 3 of its tuple — the swap overwrites them, so the tuple is the one
place they are still in hand — and then the releases, chosen as
`__fern_map_set` chooses them for the value an overwrite supersedes: a string
key through `__fern_str_free`, a keyed column's box through `krel`, a string
value through `__fern_str_free`, a string array through `__fern_str_arr_free`,
a box through `vrel`. `krel` and `vrel` are bare code addresses declared i32,
the plain-indirect call `eqfn` already takes in `__fern_map_find` — so the
#9970 note saying a Fern runtime source could not make this call was wrong,
and the body is twelve lines of Fern rather than two hand-asm walks. It is
need-gated on its own (`map_delete_rel`), so a program that deletes from an
uncounted map carries neither its body nor the string-array free it names.

`op_map_delete` grew what the set op has: `kfree` and `vkind` in `width`,
`eq|vrel|krel` packed in `str` by `ir.map_op_packed` — the same reader serves
both ops now. wasm's `$__fern_map_delete` takes `vdeep`/`vf` and `kdeep`/`kf`
as `$__fern_map_set` does and releases before tombstoning, under the box's own
`kis`/`vis`; on wasm every map owns its columns per insert, so the AST leg's
delete releases there as well, and a slot whose units are gone can be
reclaimed by a later insert without a second release.

## Measured

Five churn programs, 1000 build-and-drop rounds each, heap bump read either
side, `__rc_underflow_count()` read after, typed path, produced whole:

| column shape | x86-64 | arm64 | wasm |
| --- | ---: | ---: | ---: |
| `Map[string, i32]`, delete then re-insert the key | 0 | 0 | 0 |
| `Map[i32, string]` | 0 | 0 | 0 |
| `Map[i32, string[]]` | 0 | 0 | 0 |
| `Map[Name, i32]`, a string field in the key | 0 | 0 | 0 |
| `Map[Coord, Box]`, two deletes | 0 | 0 | 0 |

Bytes of heap growth over the 1000 rounds; every program answers what the
interpreter answers. They are `TestSelfHostMapDeleteRelease{X86_64,Arm64,Wasm}`.

The AST leg on wasm, `Map[string, string]` over six inserts, one delete:

| | bytes / 1000 rounds |
| --- | ---: |
| before | 432,000 |
| after | 400,000 |

−32 a round, the deleted entry's two boxes. The 400 that remain are that
leg's other leaks and are not this change's; `TestSelfHostMapDeleteReleaseWasmAST`
pins the delta rather than the level, the same program with and without its
delete.

`conformance/cases/map_struct_enum_keys` and `map_struct_key_grow` now
produce whole (68 of 68, 63 of 63), answer their expected 0, and read
`allocs=76 frees=76 live_bytes=0` and `102 / 102 / 0` under the sanitizer.

Corpus census, x86-64, against the compiler carrying #9962 alone:

| | before | after |
| --- | ---: | ---: |
| cases produced whole | 509 of 597 | **512 of 597** |
| declarations produced | 17,972 of 18,452 | **18,106 of 18,452** |

Exactly three files move: the two struct-key files and `map_str_delete`
(3 of 3), which was held on the AST lowering by a string-keyed delete alone.
Nothing regresses.

## What it does not reach

The register backends' AST delete still strands what it removes. Giving it
back needs the AST path's column credits at the delete site, which is the
AST lowering's own accounting; the typed path is the production route and
is where the release lives.
