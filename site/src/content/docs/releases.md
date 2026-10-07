---
title: Releases
description: How Fern ships — the nightly channel, what stability to expect before 1.0, and how to see what changed.
---

## The nightly *is* the release channel

Once a day, at 06:00 UTC, `main` is built into a rolling [**nightly
release**][nightly] with prebuilt binaries for Linux x86-64, Linux arm64
and macOS arm64. There is no tagged stable version yet, and no `latest`
that lags behind it — the nightly is what there is.

Each `fern-<platform>.tar.gz` holds one self-contained `fern` binary;
the macOS one is signed and notarized. The same release carries the
coreutils catalogue: every utility in [`coreutils/`][coreutils], GNU
coreutils reimplemented in Fern, built into one multicall binary per
platform with its symlinks laid out beside it.

Installing or updating is the same command either way; see the
[install guide](../tutorial/install/).

## What pre-1.0 means here

- **No compatibility promise.** Syntax and standard-library APIs change
  between nightlies, sometimes without a deprecation period.
- **No semantic versioning yet.** The nightly tag is reused, so "which
  nightly" is a date, not a number.
- **The language is in use, though.** Fern's compiler is written in
  Fern, and CI checks that it rebuilds itself byte for byte on all three
  host platforms. A change that breaks real programs tends to be caught
  by the largest Fern program there is, or by the coreutils, which are
  held to byte-for-byte output parity with GNU.

## Which build do I have?

```bash
$ fern -version
fern 4ae53835f86e (2026-10-07T15:33:26Z)
built with go1.27.1 for linux/amd64
```

Because the tag rolls, the commit is the answer — quote that line in a
bug report. A binary installed with `go install` prints its module
version instead, and a build from a modified checkout says so.

## Pinning a build

Because the `nightly` tag moves, pinning means keeping the artefact
rather than the tag: download the tarball once, record its
`*.tar.gz.sha256`, and install that copy everywhere. Re-downloading the
tag later will not necessarily give you the same bytes. The archives
built for every commit are also attached to its run of the
[`Build & release` workflow][runs].

The first native compile with a new `fern` builds the compiler it runs,
once, from a stage0 it downloads and verifies (see
[Install](../tutorial/install/)). For a machine without network access,
keep the `fern-selfhost` that `make bootstrap` produces alongside the
pinned `fern`.

The same applies to dependencies: `fern -resolve` writes the versions it
chose to a `fern.lock`, and `fern -vendor` flattens the resolved graph
into `vendor/` so builds stop touching the network at all.

## Seeing what changed

There is no hand-written changelog. The commit log is the record, and
it is a good one — changes land as small reviewed PRs with the reasoning
in the message:

- [Commits on `main`][commits] — every change, newest first.
- [Merged pull requests][prs] — the same work, grouped, with the
  discussion attached.
- [Open issues][issues] — what's known to be broken or missing.

A commit subject usually names the part of the project it touches
(`std/crypto: …`, `lift: …`) and the message references the issue it
closes. To follow one area, read the commit log for its path — for
example [the standard library's][stdlib-commits].

## Reporting something

If a nightly breaks a program that used to work, that's a bug worth
filing rather than a change to work around — [open an issue][issues]
with the program and the exit code. Pre-1.0 means the language moves,
not that regressions are expected.

[nightly]: https://github.com/JakeChampion/lang/releases/tag/nightly
[commits]: https://github.com/JakeChampion/lang/commits/main
[prs]: https://github.com/JakeChampion/lang/pulls?q=is%3Apr+is%3Amerged
[issues]: https://github.com/JakeChampion/lang/issues
[coreutils]: https://github.com/JakeChampion/lang/tree/main/coreutils
[runs]: https://github.com/JakeChampion/lang/actions/workflows/release.yml
[stdlib-commits]: https://github.com/JakeChampion/lang/commits/main/internal/stdlib
