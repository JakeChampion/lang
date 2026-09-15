# Reuse test binaries through the existing warmup barrier

The x86 self-host shards already wait for the warmup matrix, but each shard
then compiles the same two Go test binaries. In run 34995477746, fourteen
completed `build-e2e-binaries` steps across the lane took 18-35 seconds each.
This change lets the load warmup publish both binaries for the dependent
x86 shards. Independent jobs and ARM shards keep their local builds.

There is no additional job or dependency edge. The extra build and artifact
upload extend the load warmup itself, so their cost must be included when
evaluating the effect on the existing matrix barrier.

## Correctness

The optional artifact is scoped to this workflow run. Its manifest records
the exact checkout commit, absolute checkout path, Go version and target,
build flags, cgo configuration, runner image and libc identity. The helper
rejects tracked changes and untracked inputs other than the output binaries.
Both binary checksums are verified before either output is replaced.

A missing artifact, mismatched input key, partial upload or corrupt binary
causes the existing action to compile locally. Artifact operations are
optional; compilation and test execution retain their ordinary failure
behavior. Every shard still runs its own selected tests, with the same
targets, timeouts and outcome reporting.

The artifact name does not match the existing driver-cache download pattern.
This is not cross-run result reuse and the manifest is not intended as a
general-purpose cache key for arbitrary external build dependencies.

## Local validation

A table-driven regression suite covers matching inputs, a different commit,
tracked and untracked source edits, missing manifests, truncated binaries,
malformed digests, changed build flags, changed architecture and a changed
runner image. Misses leave both existing output files untouched. An
unavailable Go toolchain must fail before publishing an input key; a
regression reproduces Bash continuing a failed command inside a hashing
pipeline, and the helper now collects fallible key inputs before hashing.

An end-to-end pilot executes the actual composite action shell body against
a small Git repository, then against Fern at 82a544bf3. It builds both real
test binaries, saves them, removes the originals, restores them and executes
smoke tests from each package's working directory. A Go wrapper rejects any
compilation during the hit, proving the action took the restore path.
Corrupting the second binary triggers recompilation and both smoke tests
pass again. The fixture pipeline completed in under a minute before scaling
to the repository.

The native Darwin ARM64 repository run used Go 1.26.8:

| Operation | Seconds |
| --- | ---: |
| Initial build of both binaries | 15.165346 |
| Save and checksum | 0.203206 |
| Verify and restore | 0.347816 |
| Corrupt-cache fallback, warm Go build cache | 2.228131 |

The binaries were 39,945,026 and 43,349,026 bytes. These are one local
integration run's timings, not a controlled CI speedup comparison. They do
not include GitHub upload/download, compression or queueing costs.

A native Linux ARM64 pilot then repeated the same build, restore, execution
and corrupt-cache fallback sequence with Go 1.26.8 and glibc 2.36. Both smoke
tests passed after every stage. Its binaries were 38,796,747 and 41,801,477
bytes. It includes the repair for duplicate termios map entries introduced
by merging independent changes into main. The repository pilot followed a
successful sub-minute Linux fixture pilot. This verifies the Linux restore
path; native x86 GitHub transfer and execution costs still need measuring.

After rebasing onto main `b4c04aee2`, a fresh clean Linux ARM64 repository
pilot repeated the fixture-first pipeline with four CPUs and a 12 GiB memory
limit. Build, save, restore with compilation forbidden, corrupt-cache fallback
and both package smoke tests passed. This run took 17.985s to build, 0.437s
to save, 0.539s to restore and 1.935s for the warm-cache fallback. Again, these
are integration observations, not controlled speedup or GitHub transfer claims.

## Rollout

Measure load-warmup build/upload cost, per-shard download/restore duration,
cache hit rate, final suite completion and summed runner execution time.
Record image changes and fallback reasons. Compare with equivalent local
compilation under similar Go cache conditions. Retain this optimization
only if actual CI shows the added producer and transfer work is worthwhile.
