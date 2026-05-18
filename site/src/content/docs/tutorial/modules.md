---
title: Modules and imports
description: Splitting a program across files.
sidebar:
  order: 4
---

A multi-file lang program is one entry file that `import`s
siblings. The entry file is whichever `.lang` you pass to the
compiler.

## Imports

```lang
// main.lang
import "./util";

function main(): i32 {
    return util.run();
}
```

```lang
// util.lang
pub function run(): i32 {
    return 42;
}
```

The local name (`util.run`) comes from the path's basename without
the `.lang` extension. Re-aliasing isn't supported yet.

## Visibility

Top-level declarations default to private. Prefix with `pub` to
export across modules:

```lang
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

```lang
import "std/io";

function main(): i32 {
    io.println("hello");
    return 0;
}
```

The full list is under [Standard library →](../../stdlib/).

## Working with the language server

`cmd/lang-lsp` resolves imports across the whole workspace. With
the VS Code extension installed:

- Hover over `util.run()` to see the imported function's signature.
- Cmd/Ctrl-click jumps from the call site to `util.lang`.
- Rename a `pub` function and every cross-file caller updates.
