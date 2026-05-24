---
title: Modules and imports
description: Splitting a program across files.
sidebar:
  order: 4
---

A multi-file Fern program is one entry file that `import`s
siblings. The entry file is whichever `.fern` you pass to the
compiler.

## Imports

```fern
// main.fern
import "./util";

function main(): i32 {
    return util.run();
}
```

```fern
// util.fern
pub function run(): i32 {
    return 42;
}
```

The local name (`util.run`) comes from the path's basename without
the `.fern` extension. Re-aliasing isn't supported yet.

## Visibility

Top-level declarations default to private. Prefix with `pub` to
export across modules:

```fern
pub function exported(): i32 { return 0; }
pub struct Public { x: i32 }
pub enum Status { Active, Inactive }
pub const CAP: i32 = 100;

function private_helper(): i32 { return 0; }  // module-local
```

Cross-module references to a non-`pub` declaration are rejected at
load time with a diagnostic naming the offending qualifier.

## Stdlib imports

The standard library lives at `std/*` — `std/io`, `std/string`,
`std/json`, etc. Import them the same way:

```fern
import "std/io";

function main(): i32 {
    io.println("hello");
    return 0;
}
```

The full list is under [Standard library →](../../stdlib/).

## Working with the language server

`fern-lsp` resolves imports across the whole workspace. With
the VS Code extension installed:

- Hover over `util.run()` to see the imported function's signature.
- Cmd/Ctrl-click jumps from the call site to `util.fern`.
- Rename a `pub` function and every cross-file caller updates.
