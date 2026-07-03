package e2e

// Content-addressed build caches shared across every TestSelfHost* in
// one `go test` process (i.e. within a CI shard). Dozens of self-host
// tests compile the *same* ~35k-line self-host driver through the Go
// x86-64 backend and then gcc-link the result; without caching that
// full compile + link is repeated per test and is the suite's dominant
// cost. Both caches key on the exact inputs (source-set hash → asm,
// asm-hash → linked binary), so a hit returns exactly what a fresh
// build would have produced — this only deduplicates identical work
// and can never return a stale or wrong artifact. (Determinism tests —
// fixpoint / cross-validation — compare asm produced by running the
// *compiler binary* as a subprocess, not the output of these helpers,
// so the caches don't make any of those assertions tautological.)
//
// Keys deliberately exclude the in-tree stdlib + the compiler itself:
// both are fixed for the lifetime of the process, so they can't vary
// between two cache lookups in the same run.

// FERN_SELFHOST_BUILD_CACHE is an optional cross-PROCESS cache location: a
// single directory, or a PATH-list (os.PathListSeparator, ':') of directories.
// When set, the asm + linked-binary caches read from and write to it, so CI
// `warm` jobs can pre-compile the self-host drivers once and the sharded test
// jobs consume the artifacts instead of recompiling the ~35k-line compiler per
// shard — the heavy, RAM/disk-hungry work that exhausts a hosted runner mid-
// shard ("received a shutdown signal"). Empty (the default, e.g. local
// `go test`) leaves the in-process caches as the only layer. Content-addressed
// by the same source-set / asm hash as the in-process caches, so a hit is
// byte-identical to a fresh build and a stale dir only costs a miss (recompile).
//
// The list form exists because `actions/cache/restore` only populates the FIRST
// restore into a given path — restoring several caches into one shared dir
// silently drops all but the first. So each warm group is restored into its own
// dir and the shards point FERN_SELFHOST_BUILD_CACHE at the whole list; reads
// scan every dir, writes go to the first.
