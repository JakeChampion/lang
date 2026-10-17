# Scalar record returns and donor metadata

Private functions returning one through four scalar fields can return their
bits directly when the closed call graph proves every caller projects fields.
The existing sempair escape analysis supplies that proof. Records with reference
fields, finalizers, more than four fields, exported/escaping functions or function
value uses keep their boxes. Existing explicit and inferred box donations also
remain profitable: an input box a caller can update stays whole.

`semscalarreturn.fern` defines the shared protocol. The primary result and up to
three auxiliary results are u64 bits. Floating values use the existing f64
physical representation, including f32, and bit conversions preserve NaNs and
signed zero. Narrow integers and pointer-sized usize are converted explicitly.
The auxiliary registers are rdx/rcx/r11 on x86-64 and x1/x2/x3 on ARM64; WASM uses
three private i64 globals. Calls capture auxiliary words in descending order
before ownership cleanup. Returns set them in ascending order after cleanup.
These scalar bits carry no reference-counting unit. Legacy pointer-pair returns
retain their existing representation.

Negative semantic call metadata encodes `-(capture_base*4+count)`; the verifier
checks the range and types before indexing. Existing `ret_word`/`call_word` IR
operations use width 64 and an auxiliary slot index for the new form. The RC memo
epoch is 9 so old ABI judgments cannot authorize the extended protocol.

x86-64 alignment cleanup uses `lea` to adjust the stack without consuming a
return register or an incoming argument needed by a tail call. The native leaf
splicer tracks auxiliary locals explicitly. Debug-enabled builds preserve code
bytes, including the new wide-return, phi and register-pressure cases.

The allocation controls exposed an existing record-update metadata defect
(#12581): semantic renumbering rewrote operands but left the donor ID stale.
It now resolves that ID through phi aliases and the dense value map. The semantic
verifier also checks that a donor has the appropriate record type. Both explicit
spread updates and inferred donation from field reads keep their prior allocation
counts. The inferred-donor walk follows scalar dependencies and reference aliases
without crossing reference-returning calls.

## Evidence

After integrating main and the capability fixes, source `5abb1838b` reaches a
12,641,472-byte bootstrap fixed point, SHA-256
`dc58f74584b2490ec619cfba527aa69c8b58c15be9cd9015279b2f497b3adb0f`.
The focused scalar, donor, runtime-registration, inventory and complexity suite
passes (27.591 seconds); all eleven strict driver-size limits pass with complete
coverage and unchanged budgets. The broader semantic print, semantic ownership
and SSA unit suite passed on the preceding implementation (172.268 seconds).

The preceding same-protocol compiler also detects the original #12135 ownership
mutant: enabled certification exits 5 with `run: spent without a unit ... moved
into wrap`; disabling certification compiles it. The intentionally unsafe program
was never executed. This is rejection evidence, not permission to disable the
certifier.

[The recorded benchmark comparison](../benchmarks/scalar-record-returns-2026-10-17/README.md)
contains every timing, allocation count, checksum, source and compiler hash, plus
a reproduction script. Three-target ordinary and sanitized tests cover the ABI
and boxed controls. Scalar-only field multiplication callers remove 30,000 boxes;
the complete X25519 control preserves its existing 107,520 allocations.

## Integration with main 3260020a0

After the next main update, the compiler reaches the same stage-2/stage-3
fixed point at 12,699,472 bytes, SHA-256
`f395df09bc0d2fb9fa77c410384411708ac7fe941e7d56b3d6e3aeab6c5eb6b7`.
The focused scalar, sanitized, donor, path-identity, inventory and complexity
suite passes in 35.631 seconds. Declaration and instance body collection are
separate functions, as is bare-frame restoration; the complexity limits remain
unchanged. The complete strict driver-size repeat is in progress.

The tracked-size baseline moves from 12,054,832 to 12,699,472 bytes (+5.35%).
A separate checkout of main `3260020a010d15615407a7d58d917f6f0a6df8be`, built
with the previous compiler and then twice with itself, reaches identical
12,671,152-byte outputs, SHA-256
`f5539d74a09bdf471614f8e84935cdce44a5273e6e46950ac804c3652a7f83b5`.
Main already exceeds the old baseline by 5.11%; this change adds 28,320 bytes,
0.22% against that measured main. The other ten baselines and the 5% tolerance
remain unchanged. The earlier allocation comparison retains its exact original
source/compiler scope; it is not relabeled as this integrated build.
