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

## Final qualification

The final checks use source `cac925e341192154358ff3dfda3e8d11009f51f7`.
The focused driver passed on native, ARM64, and Wasm through the original strict harness.
All three executions satisfied the nonzero balanced zero-live census assertion.
The stock harness private CLI hash was not separately recorded.
No invented private CLI identity appears in this evidence.

The source gate passed five runtime sets, fixture checks, compiler modules, standard library modules, and all 85 standalone drivers.
The nonverbose source gate does not establish a separate test-node count.
The focused driver passed four TAP nodes, and unchanged complexity checks passed three nodes, with zero skips.
All 2110 format inputs, Go build, vet, workflow lint, test names, CI self-tests, and 219 source-audit nodes passed.
All 6221 source hashes and all compiler and tool hashes remained unchanged.

The Go oracle from qualified source 7872 passed check and interpreter commands on the current fixture source.
Its actual build and loaded source identities remain separate in the raw record.
The production compiler and Go implementation match that qualified source byte for byte.
The copied CLI and raw binaries retain their actual 7872 identities.
The successful make freshness check confirms that the source gate did not rebuild the compiler.
No duplicate production compiler build or separate image-corpus campaign was necessary for these test-driver changes.

One private binary compiled successfully and retained the same hash across all seven executions.
Its unmodified baseline passed with 216528 balanced allocations and zero live bytes.
The dependency controls rejected all three incorrect views with exit 166.
The instruction controls rejected those views with exit 180 and the exact limit, operand, or decode assertion message.
Those six intentional failures remain separate from ordinary passes.
Their early-exit censuses do not establish a persistent compiler leak.

The first private coordinator contained an incorrect Python conditional expression.
Python rejected it before compilation or runtime execution.
The corrected coordinator adds the missing whitespace without a source, policy, or criterion change.
The archive preserves the original script, error log, corrected script, input packets, stdout, stderr, and source identities.

The fresh unchanged whole retained-machine command failed with exit 166 before this scope.
Its source and compiler context remain explicit in the before record.
The current helper diagnostic completed successfully and captured 29 lines, exact wide operands, and 124818 balanced allocations.
The complete retained-machine command still requires later caller-proof work.
The new focused qualification does not establish a whole-group pass.

The raw records, manifests, and private-view evidence remain separate from earlier campaigns.
All gzip records preserve their exact original bytes.
All archive members match their original files byte for byte.
Executables remain at their original paths with explicit hashes.
Caller-machine integration, actual CI recovery, native milestones, M5, and epic acceptance remain open.
