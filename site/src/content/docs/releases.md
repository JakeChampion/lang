---
title: Releases
description: How Fern ships — the nightly channel, what stability to expect before 1.0, and how to see what changed.
---

## The nightly *is* the release channel

Every push to `main` publishes a rolling [**nightly
release**][nightly] with prebuilt binaries for Linux x86-64, Linux arm64
and macOS arm64. There is no tagged stable version yet, and no `latest`
that lags behind it — the nightly is what there is.

Installing or updating is the same command either way; see the
[install guide](../tutorial/install/).

## What pre-1.0 means here

- **No compatibility promise.** Syntax and standard-library APIs change
  between nightlies, sometimes without a deprecation period.
- **No semantic versioning yet.** The nightly tag is reused, so "which
  nightly" is a date, not a number.
- **The language is in use, though.** Fern's own compiler is written in
  Fern and rebuilt from `main` continuously, so a change that breaks
  real programs tends to be caught by the largest Fern program there is.

## Which build do I have?

```bash
$ fern -version
fern be167834e1b8 (2026-08-04T13:06:07Z)
built with go1.24.7 for linux/amd64
```

Because the tag rolls, the commit is the answer — quote that line in a
bug report. A binary installed with `go install` prints its module
version instead, and a build from a modified checkout says so.

## Pinning a build

Because the `nightly` tag moves, pinning means keeping the artefact
rather than the tag: download the tarball once, record its
`*.tar.gz.sha256`, and install that copy everywhere. Re-downloading the
tag later will not necessarily give you the same bytes.

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

Commits are prefixed by kind (`feat`, `fix`, `docs`, `refactor`) and
reference the issue they close, so filtering for `feat` on the commit
log is a reasonable stand-in for release notes.

## Reporting something

If a nightly breaks a program that used to work, that's a bug worth
filing rather than a change to work around — [open an issue][issues]
with the program and the exit code. Pre-1.0 means the language moves,
not that regressions are expected.

[nightly]: https://github.com/JakeChampion/lang/releases/tag/nightly
[commits]: https://github.com/JakeChampion/lang/commits/main
[prs]: https://github.com/JakeChampion/lang/pulls?q=is%3Apr+is%3Amerged
[issues]: https://github.com/JakeChampion/lang/issues
