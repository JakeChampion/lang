# 2026-10-06 — the signature tables hash with the kernel

`checker.sig_name_bucket`, the bucket of the checker's signature and method
tables, and `irtables.sig_key_bucket`, the bucket of the emitters'
label registries. Refs #8171. No emitted byte changes: the compiler before
and after builds `checker.fern` for x86-64, arm64 and wasm, and `fern.fern`
for x86-64, byte for byte, which also says that no table depends on which
bucket a name lands in: each chain is built in ascending index order, so
an exact lookup meets the same first match whichever bucket holds it.

## What changed

Both were byte rolls: the checker's masked the accumulator every
character, the registries' reduced it by the bucket count every character,
an integer division per byte, and folded two pieces so that a probe
could be hashed as a prefix and a name without joining them. Every caller
passed an empty second piece. Both tables now take
`(__str_hash(s, 0) & 0x3FFFFFFF) % n`, the word-at-a-time kernel the name
index moved to in `2026-10-06-k`.

The registries' roll also stopped at the first `|`, so a row
`<name>|<value>` shared the bucket of the bare name. Nothing needed that:
an exact lookup finds its row as long as the build and the probe hash
alike, and the bare name is rejected by the exact compare whichever
bucket it probes. The cut is gone with the roll.

## Measured

`checker.fern` built for x86-64-linux by the stage-2 compiler under
callgrind, 4-core x86-64 container. Both stage-2 compilers are built by the
same stage-1 compiler, built from main at 2e084b79 by the stage0 pin.

| | before | this change |
|---|--:|--:|
| stage 2, x86-64 target, total Ir | 16.862 G | 16.754 G (−0.64%) |
| `irtables.sig_key_bucket`, self | 73.1 M | inlined into its callers |
| `checker.sig_name_bucket`, self | 71.9 M | inlined into its callers |
| `checker.Scope.sig_at`, self | 13.3 M | 29.2 M |
| `irtables.sig_reg_has_exact`, self | 6.3 M | 14.4 M |

The two callers' self time is the kernel call and the modulus they now
hold; the 145 M the rolls cost is gone.

## What is left

The kernel's cost per call is the stack-machine bridge the register path
runs a kernel op through, as `2026-10-06-k` records: a register-path arm
for the kernel ops would take most of it off every hash in the compiler.
