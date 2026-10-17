# ARM64 constant multiplication, 2026-10-17

The first candidate replaced multiplication by 19 with two shifted adds.
[Run 39018621268](https://github.com/JakeChampion/lang/actions/runs/39018621268)
built both compilers to a fixpoint on one `macos-15` runner. Against the
expanded register pool, 15 alternating pairs measured median ratios of
1.062945 for X25519 and 1.004464 for the compiler. The X25519 regression
rejects that sequence. [The raw report](two-adds-rejected.json) retains every
timing and both compiler hashes.

The revised candidate uses shifts for powers of two, one shifted add for
powers of two plus one, and multiplication for 19. Adjacent multiplications
by 19 reuse the reserved x5 literal. Only elided immediate definitions may
separate them: other instructions and block boundaries end that known
lifetime. This does not reserve an additional allocatable register.

[The revised run, 39019596804](https://github.com/JakeChampion/lang/actions/runs/39019596804),
measures 0.906505 for X25519 (9.3% faster) and 1.032369 for the compiler
(3.2% slower), again with 15 alternating pairs each. [Its raw report](cached-literal-v2.json)
retains every timing. The next run,
[39020554239](https://github.com/JakeChampion/lang/actions/runs/39020554239),
avoids that probe on non-multiply instructions. It measures 0.932010 for
X25519 and 1.023274 for the compiler ([raw report](cached-literal-v3.json)).
The compiler cost remains unacceptable. The current candidate emits these
instruction forms as encoded records and keeps the policy marker after the
common immediate operations. Native timing for those changes is pending.

These paired timings remain separate from instruction-count, allocation,
constant-time and size gates.
