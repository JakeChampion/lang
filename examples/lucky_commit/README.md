# lucky_commit

A Fern port of [lucky-commit](https://github.com/not-an-aardvark/lucky-commit).
The tool amends the `HEAD` commit so that its hash starts with a prefix that
you choose.

```
$ git log --oneline -1
ee1f50f Some commit
$ lucky_commit
$ git log --oneline -1
0000000 Some commit
```

## Build and run

```
$ fern -O -target x86-64-linux -o lucky_commit examples/lucky_commit/lucky_commit.fern   # or arm64-linux
$ cd my-repo
$ lucky_commit             # the hash starts with 0000000
$ lucky_commit 1010101     # the hash starts with 1010101
$ lucky_commit 00_111      # `_` matches any hex digit
$ time lucky_commit --benchmark
```

The tool runs `git`, and it looks for the program at `/bin/git` and at
`/usr/bin/git`.

## How it works

The tool adds spaces and tabs to the end of the commit message. Git does not
show trailing whitespace, so the commit looks the same. The tool tries
different whitespace until the hash matches. Then it writes the new commit
with `git hash-object` and moves `HEAD` with `git update-ref`. The update
gives the old hash, so it fails if `HEAD` moved during the search.

A signed commit keeps its message, because the signature covers the
message. The tool adds the whitespace to the end of the `gpgsig` header
instead.

The whitespace has two parts:

- **Static padding.** Spaces that align the next part to a 64-byte block.
- **Dynamic padding.** 48 bytes. Each byte is a space or a tab, as set by one
  bit of a 48-bit "padding specifier".

The SHA-1 state after the blocks before the dynamic padding is the same for
each attempt. The search calculates that state one time, and then it hashes
only the last blocks for each specifier. From one specifier to the next, it
writes only the padding bytes whose bits change.

A second run on a lucky commit replaces the padding of the first run, so the
commit does not grow. A repository with the SHA-256 object format gets a
SHA-256 search.

## The search

The search runs on all CPUs, in `std/worker` workers. Each round gives each
worker a range of 4,194,304 specifiers. The search returns the lowest
specifier that matches, so the result does not change with the number of
CPUs. It is the same commit that the upstream single-threaded search finds.

`std/worker` runs a worker as a process on Linux and Android. On other
targets, it runs each worker inline, so the search uses one CPU there.

The `--benchmark` flag makes a search of 2^28 hashes that does not succeed.
On a 4-core x86-64 container with SHA-NI (2026-11-12), it takes 5.9 s. That
is about 45 million hashes per second, or 11.8 million on each core.

## Differences from upstream

- There is no GPU search. Upstream uses OpenCL when it finds a GPU, and its
  CPU search is the fallback. This port has only the CPU search.
- Upstream threads stop at the first match from any thread. This port takes
  the lowest match in each round. Thus the result is the same on each run.

## Files

| File | Contents |
|---|---|
| `lucky_commit.fern` | The command: arguments, the git calls, and `--benchmark` |
| `lucky.fern` | The hash spec, the padded commit layout, and the search |
| `lucky_test.fern` | The cases of the upstream test suite, with its expected hashes |

## Tests

- `TestSelfHostExampleLuckyCommitUnit` runs `lucky_test.fern` on x86-64,
  arm64 and wasm32-wasi, with a balanced leak census.
- `TestSelfHostExampleLuckyCommitGit` runs the command in new git repositories
  on x86-64 and arm64: an unsigned commit, a second run, a signed commit, a
  SHA-256 repository, and the error cases.

Both are in `internal/testing/e2ecompiler/self_host_example_lucky_commit_test.go`.
