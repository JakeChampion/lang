# Bounded allocation guard evidence

The source commit is `3a70b650d36331d2febe0f88d207bf0702e09ef4`.
The base is `231becae0394b07f7bdbc5cf24922b6e9db67aed`.
The summary records the exact compiler hashes and actual check results.
The manifest binds each preserved artifact to its original path and SHA-256.

The original retained driver and focused guard driver passed on native, ARM64, and Wasm with the original Go harness.
All six processes met its existing nonzero-allocation, balanced-census, and zero-live assertions.
The Go run reported seven passing nodes, zero failures, and zero skips.
These are driver execution targets, not a general proof of every backend helper.
The wider retained-machine driver and actual CI recovery remain open under #13717 and #13687.

The array-box model keeps capacities two and three.
The first-mapping model keeps requests 40 and 48 bytes.
The decoder retains the exact external abort target and condition.
The model permits that transfer only when its existing symbolic state proves it untaken.
The first-mapping carry premise requires a valid mapped region and a checked nonnegative 64-bit sum inside that region.
The initialized model and its original driver remain byte-identical to the base.

The focused controls include eleven size-guard mutations and five carry-guard mutations.
Taken guards, unknown flags, unrelated targets, malformed frames, wrong stores, and wrong widths must fail.
The combined control rejects a wrong mapping guard while the initialized path retains its separate valid premise.
All original array-box mutation assertions remain in order.
Their missing-comparison fixture now establishes unknown flags explicitly before the old comparison.
The complete current body requires a separate successful assertion.

The original main failure, emitted diagnostic, and failed candidates remain in this record.
An incorrect new initialized-path control expected an unreachable guard to invalidate that path.
The correction restored the initialized model exactly and tested the combined dependency instead.
Other setup failures came from an instruction pattern, a helper argument, and an unsupported fixture comparison.
The canonical self-test instruction corrected that last fixture without decoder expansion.
Failure-exit allocation censuses do not establish a persistent compiler leak.

The clean standard build completed stage one and its smoke tests before an environment transition interrupted the CLI process.
The missing CLI output required a separate resumed make target.
The resumed target returned make and version exit zero with immutable sources.
Its raw compiler is byte-identical to the prior qualified compiler.
The initial coordinator has no recovered outer exit status and does not count as a complete build pass.

The checks passed all 2,084 format inputs, Go build and vet, SourceLint, workflow checks, and runtime checks.
SourceLint reported 219 passing nodes with zero skips.
Both unchanged complexity gates passed with three passing nodes and zero skips.
The runtime module driver checked its five target sets and the stock feature and fixture checks completed.
The recorded runtime command uses nonverbose Go output, so its record does not claim a verbose node count.

Every production emitter, runtime source, standard-library source, baseline, threshold, and model domain remains unchanged.
The local CLI import-closure record contains no changed proof module.
The final runs used clean committed source and retained all source and binary hashes.
All actual main-recovery, image-engine milestone, and epic acceptance gates remain open.
