# Retire the release helpers superseded by typed ownership

The current producer (`ssarc.fern`) releases nested values with typed loops
and `__sem_drop_*` / `__sem_release_*` bodies. No producer remains for
`__fern_arrarr_free`, `__fern_strarrarr_free`, `__fern_optarrarr_free` or
`__fern_snapshot_dec`.

Their native assembly, WASI bundles and symbol registrations are removed.
The three deep-free needs and their dependency edges are removed too. The
surviving native `__fern_str_arr_free` still serves `set_args` and the
diagnostic exit path; its emitter no longer needs a variant parameter.
WASI's live `$__fern_arr_dec_ptr` also remains.

This continues #2649 after #12579. The allocator and array-mutator migration
remain open.

## Validation

The 54 targeted tests cover nested arrays, Option arrays, struct arrays,
aliasing, escaping rows and payloads, parameter rebinding, the complexity
ratchet and independent linking of all 124 WASI needs: zero failures or skips.
The x86-64 and WASI `set_args` cases also pass with sanitizer and balanced
allocation census. The parameter-rebinding tests now assert that the retired
snapshot symbol is absent, replacing the old check for a freelist instruction
that unrelated helpers could emit.

Eight bench programs across all four targets differ only by the deleted
snapshot body and local-label numbering. The comparison removes that named
body from the control and canonicalizes generated numeric labels; the rest
matches exactly. Example assembly/WAT byte counts for `array_append`:

| Target | Before | After |
| --- | ---: | ---: |
| x86-64 Linux | 22,097 | 21,296 |
| ARM64 Linux | 28,132 | 27,335 |
| ARM64 Darwin | 25,759 | 24,965 |
| WASI | 25,745 | 25,049 |

After integrating main's bulk-copy changes (#12576), the paired output
comparison still passes, as do the targeted nested-array, Option-array,
struct-array, snapshot, complexity and independent-WASI-link checks.

With the same pinned stage0, the compiler executable shrinks from 12,595,840
to 12,569,792 bytes, a reduction of 26,048 bytes. Bootstrap stage 2 equals
stage 3 before that integration. No baseline changes.
