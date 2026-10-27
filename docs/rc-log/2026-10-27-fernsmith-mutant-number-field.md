# Fernsmith mutant number field

Native issue #13681 tracks a malformed test plant in `scripts/fernsmith-mutant`.
Nightly job 122099389308 in run 40477547285 fails before the planted compiler can execute.
Its injected integer `ast.ExprNumber` omits the required `bits` field, and the checker reports E005.
Ordinary literals in `compiler/lift.fern` already supply that field.

The fix adds the canonical `bits: 0` value to this one injected literal.
It changes neither closure swap nor either unique source anchor.
The default 1,024-seed search limit, shrink logic, and 1,024-byte program bound remain unchanged.
The production compiler, standard library, Go test assertions, CI policy, and baselines remain unchanged.

Fresh main `5b275901e871a308b29bceec604e69200416bdc2` reproduces E005 in 3.748 seconds.
The complete corrected script exits zero in 111.317 seconds, with one passing test and no skips.
It catches a divergence at corpus 2, within the unchanged search limit.
This does not mean that all 1,024 seeds execute.

The shrinker reduces 8,192 corpus bytes to 64 in 41 attempts.
The program content after its fixed preamble reduces from 2,010 bytes to 271.
The interpreter returns 239, while the planted compiler returns 209.
The unchanged control requires the unplanted compiler to agree with the interpreter.
The 271-byte result remains below the existing 1,024-byte bound.

Bash syntax and the embedded Python syntax checks pass.
An exact diff check confirms that the added field is the only script change.
The current clean compiler is reused after compiler and standard-library source identity checks.
Its SHA256 is `6e95755acdba854fc94ec32aaa37d09dd8264bf4438b9952e37cc198bac5d9ab`.
No compiler implementation change requires another bootstrap build.

The [proof directory](../benchmarks/fernsmith-mutant-2026-10-27/) retains the actual failure, successful script log, and source checks.
Its records explicitly identify the uncommitted script phase and exact script hash.
A local copy preserves the actual planted compiler and modified lift source before temporary cleanup.
The manifest records their hashes, without adding that intentionally incorrect compiler to the repository.

Pending, cancelled, and skipped CI do not establish a validation pass.
This harness repair does not establish general compiler, image-engine, or production acceptance.
