# Current retained helper controls

Issue #13753 repairs two test-driver expectations for the current large-free helper.
The helper starts with MOVABS 17179869184 into RAX and a comparison of RAX against RSI.
The selected body contains exactly 29 instructions.
The old actual-output assertions expect a single immediate comparison and 28 instructions.
The old decoder records an unknown first instruction for the current body.

The actual-output assertions now use the accepted wide decoder.
They require the complete kind sequence and one instruction per selected line.
They also require the exact I64 limit, guard operands, branch-label identities, freelist symbol, indexed-load operands, and store operands.
The dependency-boundary assertion requires the exact first two lines.
The capacity-helper assertion remains unchanged.

All original sample, address, shift, literal, unsupported-instruction, isolation, namespace, and boundary controls remain in their original order.
Their function bodies remain byte for byte unchanged.
The old decoder APIs, caller models, numeric domains, production limits, baselines, and gate thresholds remain unchanged.
The new focused driver emits the actual helper and runs both original control groups.

Three private views contain an incorrect limit, swapped comparison operands, or an unsupported first instruction.
One compiled binary checks every view against both control groups.
Its unmodified baseline must pass with a nonzero balanced zero-live census.
Each incorrect view must fail with its declared assertion message and exit code.
Those intentional control exits are separate from ordinary successful qualification.
Their early-exit censuses do not establish a persistent compiler leak.

This scope changes test drivers only.
It preserves the qualified production compiler implementation and records the actual build identity of each reused tool.
The complete retained-machine command still requires separate caller-proof work.
Full recovery, actual CI recovery, M5, and epic acceptance remain open.
