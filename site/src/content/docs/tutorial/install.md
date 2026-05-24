---
title: Install
description: Build the Fern toolchain from source.
sidebar:
  order: 1
---

Fern is a Go-built compiler. Until prebuilt binaries land, build
from source — it's a one-command compile.

## Prerequisites

- **Go 1.24+** ([download](https://go.dev/dl/)). The compiler is a
  single Go module with no external dependencies.
- **A C linker** for native targets (`cc` is fine; `clang` and
  `gcc` both work). Optional — only needed if you want to produce
  ELF / Mach-O executables. The WASM target needs no linker.

## Build the toolchain

```bash
git clone https://github.com/JakeChampion/lang
cd lang
go build -o ~/.local/bin/fern ./cmd/fern
```

That's it. `fern` is now on your `PATH`. Verify:

```bash
fern -help
```

### Companion binaries

| Binary       | Build command                          | Purpose                          |
| ------------ | -------------------------------------- | -------------------------------- |
| `fern`       | `go build ./cmd/fern`                  | The main compiler + runner.      |
| `fern-lsp`   | `go build ./cmd/fern-lsp`              | Language server for editors.     |
| `dump_arm64` | `go build ./cmd/dump_arm64`            | Disassemble an emitted .s file.  |
| `dump_wat`   | `go build ./cmd/dump_wat`              | Inspect a .wat module.           |

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

Or compile to wasm and execute under wasmtime:

```bash
fern -target wasm -o hello.wasm hello.fern
wasmtime hello.wasm
```

[Next: First steps →](../first-steps/)
