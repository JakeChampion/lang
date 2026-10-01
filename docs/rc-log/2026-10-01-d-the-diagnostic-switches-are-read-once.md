# 2026-10-01 — the diagnostic switches are read once per emission

`asmcore.EmitState.rc_free_debug`, `asmcore.rc_free_debug_on`,
`asmcore.sanitize_on`, `ssarc.Emit.no_reuse`. Refs #8171.

## An environment walk per instruction

`__fern_env` walks the process environment and compares every entry's name;
one read is a few thousand instructions. The x86-64 and arm64 SSA emitters
asked `rc_free_debug_on()` — two reads, `FERN_SANITIZE` then
`FERN_RC_FREE_DEBUG` — at every rc primitive (`ssa_rc_prim`, `ssa_rc_is_one`,
arm64's `san_poison_check` three times per primitive), every call
(`ssa_call`) and every runtime call (`ssa_rt_call`, twice), and
`ssarc.reuse_pairs` read `FERN_SELFHOST_NO_REUSE` per block. On the stage-2
compile of `checker.fern` that was 1.01% of all instructions in `__fern_env`
alone, before the `Option` each read hands back.

The switches are properties of the compiler process, so they are read where
the emission state is made: `asmcore.new_state` stores `rc_free_debug`, the
emitters test the field, and `ssarc.lower` reads the reuse switch once into
`Emit` for `block_body` and `reuse_pairs`. The two reader functions moved from
the backends, which each had an identical copy, into `asmcore`; the string
helpers that have no state in hand (`quarantine_rc`, `san_poison_check` on
x86-64, `arr_push_fast`) still call `asmcore.rc_free_debug_on()`, once per
runtime helper emitted.

## Measured

`checker.fern` to a binary under callgrind, 4-core x86-64 container, both
rows on the same source. "Native-built" is the compiler `bin/fern` builds,
"stage 2" the one the self-host compiler builds from the same commit.

| | main (92089cb) | switches read once |
|---|--:|--:|
| native-built, total Ir | 70.19 G | 70.03 G (−0.2%) |
| native-built, `__fern_env` self Ir | 155.5 M | 10.5 M |
| stage 2, total Ir | 41.87 G | 41.46 G (−1.0%) |
| stage 2, `__fern_env` self Ir | 423.6 M | 28.7 M |

Byte-identical: the `checker.fern` binary, the `-emit asm` text on both
native targets under each of `FERN_SANITIZE`, `FERN_RC_FREE_DEBUG`,
`FERN_SELFHOST_NO_REUSE` and `FERN_LEAKCHECK`, and all 1,959
`selfhost-emit-hashes` rows, against a compiler built from main.

## Left alone

`irlower.lower_block` reads the reuse switch per block and `lower_func`
twice per function; the AST lowering is being retired
(`docs/SELFHOST-SEMANTIC-SOURCE.md`) and the remaining `__fern_env` share is
0.07%, most of it `ssarc.lower`'s read per function and `semlower`'s
per-module knobs.

## Next

On the stage-2 profile after this: a record update that reuses its donor in
place still loads and stores every field (`ssarc.reuse_construct`), about
900 Ir per `X86Asm` update in the assembler; the assembler's per-round
`x86_str_contains` tests on operands (`x86_gas_mem_op`, 1.0%) and the
`x86_gas_trim` copies in `x86_gas_prepare`.
