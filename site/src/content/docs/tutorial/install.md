---
title: Install
description: Get the Fern toolchain — prebuilt binaries or a one-command build from source.
sidebar:
  order: 1
---

There are two ways to get `fern`: download a prebuilt binary, or build
from source with Go. Both take about a minute.

## Option A — prebuilt binary (fastest)

A rolling [**nightly release**][nightly] is rebuilt from `main` once a
day, at 06:00 UTC, with statically-linked binaries. Grab the one for
your platform:

| Platform              | Asset                          |
| --------------------- | ------------------------------ |
| Linux x86-64          | `fern-linux-x86_64.tar.gz`     |
| Linux arm64           | `fern-linux-arm64.tar.gz`      |
| macOS (Apple Silicon) | `fern-darwin-arm64.tar.gz`     |

```bash
# Linux x86-64 — swap the asset name for your platform.
curl -fsSL -o fern.tar.gz \
  https://github.com/JakeChampion/lang/releases/download/nightly/fern-linux-x86_64.tar.gz
tar -xzf fern.tar.gz
install -m755 fern ~/.local/bin/fern    # anywhere on your $PATH
```

Each asset ships a `*.tar.gz.sha256` alongside it if you want to verify
the download. The archive holds only the `fern` binary; the compiler it
runs is built on your machine the first time you compile (see
[The first compile](#the-first-compile)). [Releases](../../releases/)
covers what the nightly channel promises and how to pin a build.

## Option B — build from source

If you have Go, install straight from the module path:

```bash
go install github.com/jakechampion/lang/cmd/fern@latest
```

Or clone and build — useful if you also want the companion tools:

```bash
git clone https://github.com/JakeChampion/lang
cd lang
go build -o ~/.local/bin/fern ./cmd/fern
```

Building needs **Go 1.26+** ([download](https://go.dev/dl/)) and nothing
else. Compiling a Fern program needs nothing else either — no `gcc`, no
`clang`, no `ld`.

## The first compile

`fern` compiles through Fern's self-hosted compiler, which is written
in Fern. (`fern -interp` and `fern -check` do not need it.) It uses
`$FERN_SELFHOST` when that is set, else a `fern-selfhost` in the same
directory as `fern`. With neither, the first compile downloads the stage0
compiler pinned in `bootstrap/stage0.lock` from the project's GitHub
releases, checks its sha256, and builds the self-hosted compiler from the
sources inside `fern`. That takes about a minute, once per version; the
result is cached under your user cache directory (`~/.cache/fern` on
Linux, `~/Library/Caches/fern` on macOS).

To compile on a machine without network access, build the compiler once
from a checkout where you have it:

```bash
make bootstrap                       # writes bin/fern-selfhost
export FERN_SELFHOST=$PWD/bin/fern-selfhost
```

or copy `bin/fern-selfhost` next to `fern`.

## Verify the install

```bash
fern -version    # the commit this binary was built from
fern -targets    # every compile target and what its host provides
```

### Companion binaries

These are built from a source checkout:

| Binary          | Build command              | Purpose                                                                  |
| --------------- | -------------------------- | ------------------------------------------------------------------------ |
| `fern`          | `go build ./cmd/fern`      | The CLI: interpreter, checker, formatter, linter, package tools; runs the compiler for `-target` builds. |
| `fern-selfhost` | `make bootstrap`           | The compiler itself, built without Go from the pinned stage0.            |
| `fern-lsp`      | `go build ./cmd/fern-lsp`  | Language server for editors.                                             |
| `ferndoc`       | `go build ./cmd/ferndoc`   | Generate the stdlib reference.                                           |

## Run hello, world

Save this as `hello.fern`:

```fern
function main(): i32 {
    print("hello, world");
    return 0;
}
```

Run it under the interpreter:

```bash
fern -interp hello.fern
```

Compile a native binary. The default target is `arm64-linux`, so name the
target that matches your machine:

```bash
fern -target x86-64-linux -o hello hello.fern    # or arm64-linux, arm64-darwin
./hello
```

`fern -target x86-64-linux -run hello.fern` builds to a temporary binary
and runs it in one step.

Or compile to wasm and run under wasmtime:

```bash
fern -target wasm32-wasi -o hello.wasm hello.fern
wasmtime run hello.wasm
```

[Next: First steps →](../first-steps/)

[nightly]: https://github.com/JakeChampion/lang/releases/tag/nightly
