# Main merge for current retained-helper controls

The branch incorporates main at `369e2f6f31d7c7b8a859bb61a7ee34bfd2ef98d6` through merge `f2f6a166a3b08e268c9ee0ca6f58b0808cbe4ff5`.
PR #13754 repairs two test-driver expectations for the current wide helper.
All 13 merged files match main byte for byte.
All 1,067 production compiler and standard-library files retain their prior bytes.
The original sample-control function bodies and their order also remain byte-identical.

The combined branch passes the focused retained-helper controls on x86-64, ARM64 and Wasm.
The import-free check and focused controls report five successful nodes, zero failures and zero skips.
The target executions require nonzero balanced allocation censuses and zero live bytes.
Source, feature, unchanged complexity and all 2,110 formatter inputs pass.
The raw records contain the complete commands and actual exit codes.

This merge changes test drivers and documentation only.
The production source identities preserve the basis of the current compiler profile.
No duplicate compiler build or profile supplies a new performance claim.
The separate caller-machine integration and complete retained-machine qualification remain open under #13717.
The published head requires its own applicable CI, and issue #4412 remains incomplete.
