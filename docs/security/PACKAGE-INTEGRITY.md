# Package integrity, lock files and provenance

Status: [reference] audit and fix for [SEC-044](https://github.com/JakeChampion/lang/issues/13546).
Parent: [security epic](https://github.com/JakeChampion/lang/issues/13502).
Source audit: the follow-on branch of #13677, dated 2026-10-27.

## What a package is

A dependency is a path, a workspace member, a URL with a hash, or a version.
The SHA-256 of the archive identifies a URL dependency, written as `sha256:` and 64 lowercase hex digits.
The URL is a mirror hint.
Minimum version selection over an index file resolves a version dependency, and `fern -resolve` writes the chosen set to `fern.lock`.
A build reads only `fern.lock` and the package store.
It never reads the index and never opens the network (`docs/PACKAGES.md`).

## Where the checks are

| Boundary | Self-host compiler | Go oracle |
|---|---|---|
| Hash spelling at every parse | `fern_toml.validate_hash` | `pkgcache.ValidateHash` |
| Archive bytes against the declared hash | `pkgfetch.fetch` | `pkgcache.Fetch` |
| Archive size and unpacked size | `MAX_ARCHIVE` (64 MiB) and `MAX_UNPACKED` (512 MiB) in `compiler/pkgfetch.fern` | the same limits in `pkgcache` |
| Archive entries | `write_entries` refuses a name that escapes the root, a symbolic link, and every type but a file or a directory | `unpackInto` |
| Store path | `modloader.store_dir` builds the path from a validated hash only | `pkgcache.Dir` |
| Lock entry against the manifest | `modloader.locked_dir` | `modload.lockedDepDir` |

## What the audit found

The lock lookup found an entry by name and read nothing else.
A manifest that raised its demand after `fern -resolve` ran kept building with the older locked version.
The lock was stale and nothing said so.
Both loaders now compare the locked version with the manifest's minimum.
A locked version below the minimum is refused with the message `dependency "NAME" is locked at X but DIR/fern.toml demands at least Y`, and the message names `fern -resolve`.
A lock at or above the minimum loads, because minimum version selection keeps the maximum of the minimums.

## Negative tests

| Attack | Test | Outcome |
|---|---|---|
| Altered archive | `TestShippedFernFetchRefusals/hash-mismatch`, `TestFetchRejectsHashMismatch` | refused, nothing unpacked |
| Entry that escapes the package directory | `TestShippedFernFetchRefusals/escaping-entry`, `TestFetchRejectsEscapingEntries` | refused |
| Symbolic link in the archive | `TestShippedFernFetchRefusals/symlink` | refused |
| Not an archive, or a truncated archive | `TestShippedFernFetchRefusals/not-gzip`, `TestShippedFernFetchRefusals/truncated` | refused |
| Hash spelled to reach a path outside the store | `TestDirRejectsTraversalAndMalformedHex`, `TestReadLockRejectsMalformedHash`, `TestParseIndexRejectsMalformedHash`, `TestLockHashOutsideStoreFailsBuild` | refused at the parse boundary |
| Locked version absent from the store | `TestLoadManifestURLDepUnfetchedErrors` | refused, names `fern -fetch` |
| Dependency absent from the lock, or no lock | `TestSelfHostDeclaredImportResolution` and its Go rows | refused, names `fern -resolve` |
| Lock below the manifest's demand | `TestLoadStaleLockErrors/stale`, `TestSelfHostStaleLockRefused` | refused, names `fern -resolve` |
| Lock at or above the demand (controls) | `TestLoadStaleLockErrors/at-demand`, `TestLoadStaleLockErrors/above-demand`, `TestSelfHostMvsCachedURLDependency` | loads |
| Index with no signature file, altered index, signature by another key, malformed signature | `TestResolveSignedIndex`, `TestSelfHostSignedIndexResolve` | refused, no lock written |
| Index key not `ed25519:` and 64 lowercase hex digits | `TestIndexKey`, `TestSelfHostManifestParity/malformed index key` | refused at the parse boundary |

## Signatures and rollback

A hash pins the bytes and nothing more.
It does not say who published the archive. It does not stop a mirror from serving an older archive whose hash a manifest still names.
The owner chose the signed index on 2026-10-28, from three choices: hash pinning only, a signed index, or a signature on every archive.

A detached signature over `index.toml` with a key the manifest names binds every version-to-hash row to a publisher.
`fern -resolve` verifies it before it reads the index. The build is unchanged.

### The signed index

The manifest names the publisher's key under `[package]`:

```toml
[package]
name = "app"
index = "index.toml"
index_key = "ed25519:9f3c…"   # 64 lowercase hex digits of the public key
```

The signature lives beside the index, in `index.toml.sig`, as 128 hex digits of the Ed25519 signature over the exact bytes of `index.toml`.
Whitespace around the digits is ignored.
A publisher signs with any Ed25519 tool. With OpenSSL:

```sh
openssl pkeyutl -sign -rawin -inkey publisher.pem -in index.toml | od -An -v -tx1 | tr -d ' \n' > index.toml.sig
```

`fern -resolve` reads the index once, verifies the signature over those bytes, and then parses them.
A manifest with no `index_key` resolves as before.

The refusals name the file:

| Condition | Message |
|---|---|
| The key is not `ed25519:` and 64 lowercase hex digits | `index_key: must be `ed25519:` + 64 lowercase hex digits of the public key, got "…"` at the manifest parse |
| No `index.toml.sig` | `index IPATH: fern.toml names index_key but IPATH.sig cannot be read` |
| The signature file is not 128 hex digits | `index IPATH.sig: must be 128 hex digits of the ed25519 signature over the index` |
| The index changed, or another key signed it | `index IPATH: the signature in IPATH.sig does not verify against index_key` |

Both loaders print the same words. `TestSelfHostSignedIndexResolve` runs the shipped compiler and the Go oracle over the same six trees and compares their output and their lock.
`TestResolveSignedIndex` is the Go side alone, and `TestIndexKey` with the `index key` rows of `TestSelfHostManifestParity` pin the key spelling.

Rollback is a resolve-time decision, because the build never reads the index.
A lower version that a manifest demands is never selected over the lock without a new `fern -resolve`, and the stale-lock refusal stops the other direction.
A signed index that a mirror rolls back to an older signed copy still verifies. The signature binds the rows to the publisher, not to a date.

## Limits

- The signature covers the index, not the archives. The hash alone pins an archive.
- No `fern` command signs an index. The publisher signs with an external Ed25519 tool.
- A rolled-back index that the publisher signed verifies. A date or a counter in the index needs a format change.
- The archive limits bound one archive. A manifest can declare many.
- `fern -resolve` reads the index file from disk or fetches it over HTTPS. The TLS stack's own audit is `CRYPTO-AUDIT.md`.
