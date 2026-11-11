# Refusal validation and archive restoration

Status: [record] source integration and file restoration on 2026-11-11.

Concurrent commit `4e8cbe42` merged main into PR #15236.
It also removed the refusal harness and 33 files from the checker cliff archive.
The archive README remained.
The current repair preserves that concurrent commit as an ancestor.

The missing files return from publication `34229724`.
They retain the original 31 data hashes, twenty gzip identities, and strict verifier.
The later integration archive retains all 72 data hashes and 63 gzip identities.
Its README now pins verification to the original publication.
These are committed source files, not recovered ignored artifacts.

The refusal harness also matches `34229724` exactly.
It validates production compiler refusals without a change to fixture sources, proof rules, coverage floors, budgets, or tolerances.
Compiler, standard library, checker, syntax, conformance, and upstream CI sources match main `eb264b32`.
The repair retains all owner changes from main.

## Validation

The source check and selected commands pass.
The complete Go checker and parser, native controls, classifier controls, fixtures, and index tests pass 10,896 test/subtest results.
The results contain zero skips.
Go format, Go vet, whitespace checks, and the complete CI self-test also pass.

The first private collector used an incorrect fixture-family expectation.
Its five child commands passed, but its metadata assertion failed.
The continuation retained that failure and ran only the five commands that remained.
No passed command repeated.
The archive includes the original failure, fixture-domain audit, continuation, commands, and raw outputs.

The first native controller stopped before any workload because the private collector failed.
The complete IR sweep passes all 1,334 current fixtures with zero skips.
Fresh baseline and candidate normal compiler images match byte for byte.
Their complete Opus executable images also match, and the program matches the full reference TAP output.
Six production compiler controls require the E010 and E091 refusals on x86-64, ARM64, and WebAssembly.
Each refusal returns diagnostic status 1 and leaves no output executable.

Stage-2 and stage-3 compiler images match byte for byte.
This secondary control cannot detect a stable miscompile.
The source inventory, producer pin, flags, raw commands, and outcomes remain in this record.

The workspace filesystem filled before the new evidence folder existed.
All native controls still completed with their expected exits.
New work uses a separate tmpfs checkout with the exact staged tree.
The original workspace files remain intact, and no passed test repeated.

## Historical measurements

The checker cliff baseline remains 373796, with its original dated evidence.
The repair changes no numeric baseline or tolerance.
The earlier counter reports retain their original source and publication pins.
This record establishes no compiler speedup, normal leak freedom, or external rank.

## Verification

Run from the repository root at this publication:

```sh
python3 docs/benchmarks/compiler-refusal-restoration-2026-11-11/verify.py --commit ee49011154090b713533a402dbbf6a73c0c9c3f1
```

The verifier checks the restored records, refusal harness, current source trees, baselines, controls, and raw gzip identities.
After another source change, pin this command to the original publication commit.

## Later source integration

The later merge retains main `d775b3e9`, with the owner changes intact.
The records above still qualify publication `ee490111` and its original source.
The verifier and all 75 data records remain unchanged.
