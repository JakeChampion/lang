# Reuse the initializer type in destructuring declarations

Part of #11534 and #8171.

The checker gets the initializer type before it binds a tuple or record pattern.
The binder now uses that type for the whole pattern and for each field.
It resolves field names in the scope before the declaration.
It retains the qualified enum lookup, generic field substitution, and tuple index checks.

## Stage-2 measurements

Two compilers built the same sources from `84ea3a5782e7` for x86-64 Linux.
Each compiler used the pinned stage 0 and one self-compile.
Callgrind counted instructions with verification enabled.

| Fixed source | Main instructions | Branch instructions | Instructions saved |
| --- | ---: | ---: | ---: |
| `destructure_tuple_and_struct_forms/main.fern` | 20,074,347 | 19,972,482 | 101,865 |

Both compilers produced the same native binary for the fixture.
The focused suite covered 24 programs on x86-64 Linux, ARM64 Linux, and WASI.
Both compilers produced the same bytes and execution results for all 72 comparisons.
For the WASI case with exit code 136, a wrapper checked the full return value.
All 12 error fixtures produced identical JSON diagnostics.
