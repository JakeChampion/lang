# `pub(package)` visibility

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

A package is a **directory**. Two modules are in the same package iff
they live in the same directory. The whole embedded standard library
(`std/…` + `core/…`) is treated as **one** package.

This needs no package-manifest or package-manager concept: the boundary
falls out of the filesystem layout that already determines module
identity.

## Why

`pub` was binary — a decl was either module-private or world-public. That
forced internal helpers to be fully `pub` just so a sibling module could
use them, leaking implementation detail into the public surface. The
clearest example is the stdlib: `core/int.fern` exports `__`-prefixed
helpers like `__int_to_string_u64` purely so other stdlib modules can call
them. With `pub(package)` those can be visible across the stdlib without
being exported to user programs. (Migrating the stdlib to use it is a
follow-up; this change adds the capability.)

`pub(package)` pairs with `pub use` re-exports: a module keeps its
internals `pub(package)` and re-exports only a curated set of names.

## Errors

Referencing a `pub(package)` decl from a different package is a load-time
error:

```
helpers.helper is `pub(package)` — only modules in the same package as helpers may use it
```

## Implementation

- `ast`: every declaration kind gains `PackageScoped bool` (mutually
  exclusive with `Public`).
- Parser: `pub(package) <decl>` sets `PackageScoped`; `pub` still sets
  `Public`. `pub(<anything-else>)` is a parse error.
- `modload`: each module records its `pub(package)` names
  (`module.packageScoped`); the cross-module visibility gates
  (`checkPublicFunc` / `checkPublicStruct` / `checkPublicValue`) allow a
  package-scoped name when `samePackage(importer, target)` holds —
  directory equality, or both inside the stdlib.
- Printer: `fern -fmt` round-trips `pub(package)`.

## Follow-ups

- Migrate the stdlib's `__`-prefixed cross-module helpers from `pub` to
  `pub(package)` and drop them from the public surface.
- Self-host compiler support (the self-host parser doesn't parse
  `pub(package)` yet; no stdlib uses it, so the bootstrap is unaffected).
- Finer scopes (`pub(super)` / explicit package roots) if a user-facing
  package system later lands.
