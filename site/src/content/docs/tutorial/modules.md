---
title: Modules and imports
description: Splitting a program across files.
sidebar:
  order: 4
---

A multi-file Fern program is one entry file that `import`s
siblings. The entry file is whichever `.fern` you pass the
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

The local name (`util.run`) is the path's basename without the
`.fern` extension. `as` picks a different one:

```fern
import "./util" as u;

function main(): i32 {
    return u.run();
}
```

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
load time with a diagnostic naming the offending qualifier:

```
main.fern:2:35: error: util.private_helper is not exported (declare it as
`pub function private_helper …` to make it accessible from other modules)
```

A module can re-export names from another with `pub use`, so callers
import one facade instead of every file behind it:

```fern
// shapes.fern
pub use "./util".{run, Public};
```

After `import "./shapes";`, `shapes.run()` and `shapes.Public` resolve to
the definitions in `util.fern`.

## Stdlib imports

The standard library lives at `std/*` — `std/io`, `std/string`,
`std/json`, `std/http`, and so on — with a few low-level modules under
`core/*` (`core/map`, `core/int`). Import them the same way:

```fern
import "std/path";

function main(): i32 {
    print(path.path_join(["docs", "tutorial", "modules.md"]));
    return 0;
}
```

There is no prelude: a program sees only what it imports, and that
includes methods. `import "std/string";` is what gives every `string`
its `.trim()` and `.split()`, and `import "std/i32";` gives an `i32` its
`.to_string()`. Methods are called without the module prefix.

The full list is under [Standard library →](../../stdlib/).

## Working with the language server

`fern-lsp` resolves imports across the workspace. With the VS Code
extension installed:

- Hover over `util.run()` to see the imported function's signature.
- Cmd/Ctrl-click jumps from the call site to `util.fern`.
- Rename a `pub` function and every cross-file caller updates.

[Next: Build a CLI tool →](../cli-tool/)
