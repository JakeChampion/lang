---
title: Install
description: Get the Fern toolchain — prebuilt binaries or a one-command build from source.
sidebar:
  order: 1
---

There are three ways to get `fern`: download a prebuilt binary, build from
source with Go, or bootstrap the self-hosted compiler with no Go at all.
The first two take about a minute.

## Option A — prebuilt binary (fastest)

A rolling [**nightly release**][nightly] is built once a day from `main`,
with statically-linked binaries. Grab the one for your platform:

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
the download. [Releases](../../releases/) covers what the nightly
channel promises and how to pin a build.

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
`clang`, no `ld`. Pass `-cc` (for example `-cc clang`) if you would
rather use your own assembler and linker.

## Option C — bootstrap, with no Go on the machine

Fern's compiler is written in Fern, so it can build itself. A pinned
earlier compiler (**stage 0**) is downloaded, verified against the sha256
in `bootstrap/stage0.lock`, and used to compile the current sources:

```bash
git clone https://github.com/JakeChampion/lang
cd lang
make bootstrap        # → bin/fern-selfhost
```

No Go toolchain and no native backend are needed on the machine running
it. [Self-hosting and bootstrap](../../compiler/bootstrap/) explains what
the pin is, how to verify it by hand, and which reproducibility gates are
green.

## Verify the install

```bash
fern -help
```

### Companion binaries

These are only built from a source checkout (`go build ./cmd/...`):

| Binary       | Build command                          | Purpose                          |
| ------------ | -------------------------------------- | -------------------------------- |
| `fern`       | `go build ./cmd/fern`                  | The main compiler + runner.      |
| `fern-lsp`   | `go build ./cmd/fern-lsp`              | Language server for editors.     |
| `ferndoc`    | `go build ./cmd/ferndoc`               | Generate the stdlib reference.   |
| `dump_arm64` | `go build ./cmd/dump_arm64`            | Disassemble an emitted .s file.  |

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

Or compile to wasm and run under wasmtime:

```bash
fern -target wasm32-wasi -o hello.wasm hello.fern
wasmtime hello.wasm
```

[Next: First steps →](../first-steps/)

[nightly]: https://github.com/JakeChampion/lang/releases/tag/nightly
