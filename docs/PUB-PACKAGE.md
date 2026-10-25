# `pub(package)` visibility

Status: [reference] the `pub(package)` visibility level, as shipped.

Fern decl visibility has three levels:

| Form | Visible to |
| --- | --- |
| (none) | the declaring module only |
| `pub(package)` | the declaring module + other modules in the **same package** |
| `pub` | everywhere (exported to all consumers) |

```fern
pub(package) function helper(n: i32): i32 { return n + 1; }
pub function api(n: i32): i32 { return helper(n) * 2; }
```

## What is a "package"?

A package contains the files governed by the nearest `fern.toml`.
Subdirectories share that package until a nested manifest starts another package.
The manifest path determines identity. Two manifests with the same package
name still define different packages.

Without a manifest, a package is one directory. A subdirectory then defines
a different package. The standard library (`std/…` and `core/…`) is one
package across its directories.

For example, `app/main.fern` can call a `pub(package)` helper in
`app/lib/parse.fern` when `app/fern.toml` governs both files.
A separate `app/lib/fern.toml` prevents that access.

## Why

The two original visibility levels forced shared internal helpers to use `pub`.
For example, other library modules call `core/int.__int_to_string_u64`.
The `pub(package)` level permits those calls without a public declaration.

`pub use` can re-export public declarations. It cannot expose a
`pub(package)` declaration through a public facade.
An enum variant stays under its enum when that enum is re-exported.
Both loaders check every directive before they combine module declarations.
Unused names and chains receive the same checks.
Invalid directives report E087 at the source directive.

## Errors

A reference to a `pub(package)` declaration from another package causes a
load-time error:

```
helpers.helper is `pub(package)` — only modules in the same package as helpers may use it
```

## Implementation

- Both parsers and checkers support `pub(package)`.
- Each loader records the package identity before it changes symbol names.
- Visibility checks compare cached identities without another manifest search
  for each reference.
- Enum constructors and qualified patterns follow the visibility of their enum.
- `fern -fmt` preserves `pub(package)`.

## Standard library

The Map runtime helpers, integer conversion helpers, UTF-8 helpers, JSON
derive helpers, and `pvec.VNode` use `pub(package)`.
Library modules can share these declarations. User programs cannot name them
through a library qualifier.

Compiler-generated `FromJson` methods retain access to the JSON derive helpers.
Source-written methods follow the normal visibility rule.

The `f64.pow10_*` tables, decimal buffer writers, `digit_pairs`, and
`io_buffered.BufBlock` also use `pub(package)`.
The table test runs within the library package and checks all 619 entries.
Public writer methods provide buffered output.
The `OrdNode`, `HNode`, and `OrdSplit` declarations no longer form a public
surface in the current library.
