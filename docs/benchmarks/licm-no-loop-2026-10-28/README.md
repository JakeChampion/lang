# LICM scratch storage comparison

Both stage-2 compilers use one stage-1 producer and the same frozen checker and standard library.
The baseline artifact comes from source `b72381ec50d4390a703d17096a4a61deae25cb2f`.
The production-source preservation record confirms the same compiler sources after the later test-driver merge.
The archived source tree includes those driver changes at `f2f6a166a3b08e268c9ee0ca6f58b0808cbe4ff5`.
The candidate changes only `compiler/ir.fern` in that tree.

The baseline executes 24,977,303,274 instructions, and the candidate executes 24,975,255,111 instructions.
The decrease is 0.008200% on the fixed subject.
Box calls decrease from 17,398,916 to 17,397,831.
Calls to `ir.cp_max_slot` decrease from 9,199 to 8,078.
All three complete checker outputs remain byte-identical.
This result does not establish a native latency improvement.

The raw Callgrind files, symbol tables and resolved reports retain attribution for each binary.
The load-equivalence records compare each release binary with its counterpart with symbols.
The build and comparison records retain commands, binary hashes and the common producer identity.
The baseline archive, source hashes and candidate patch identify the source inputs.
The archive manifest records raw byte counts and hashes.

The [fixed subject archive](../unit-successor-count-2026-10-28/fixed-subject-source.tar.gz) preserves the checker and standard library.
The [subject manifest](../unit-successor-count-2026-10-28/fixed-subject-hashes.json) records their input hashes.
The sanitized differential has 350 cases per target before and after the change.
Every run returns zero with identical output, nonzero balanced allocation counts and zero live bytes.
The raw differential source and all six output and census records remain in this archive.

The [SSA log](../../ssa-log/2026-10-28-licm-no-loop.md) explains the reverse traversal and complete slot bound.
This measurement remains separate from earlier producer groups.

The primary LICM suites pass 38 nodes, with zero failures and zero skips.
Source, feature, unchanged complexity and all 2,110 formatter inputs pass.
The validation records retain the actual process exit codes and source hashes.
Full current-head CI, retained-machine integration and the architecture roadmap remain incomplete.
