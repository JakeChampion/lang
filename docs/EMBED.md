# Asset embedding

**Status: [reference]** — shipped. Issue
[#6069](https://github.com/JakeChampion/lang/issues/6069), sub-issue (b) of
the single-file-distributable epic
[#6067](https://github.com/JakeChampion/lang/issues/6067).

Fern embeds assets into the binary at **compile time**. A directory is
handed to the compiler; each file becomes an ordinary string literal in the
program.

```
fern -embed ./assets -target x86-64-linux -o prog prog.fern
```

```fern
const PAGE: string = __fern_asset("html/index.html");

function main(): i32 {
    print(PAGE);
    var icon: string = __fern_asset("img/favicon.png");
    return 0;
}
```

Names are slash-separated paths **relative to the embed root**, on every
host OS: `-embed ./assets` publishes `assets/html/index.html` as
`"html/index.html"`.

## Enumeration

`__fern_assets()` yields every embedded asset as a `(name, contents)`
tuple, in sorted-name order:

```fern
function main(): i32 {
    for a in __fern_assets() {
        print(a.0);          // name
        serve(a.0, a.1);     // contents
    }
    return 0;
}
```

Use `.0` / `.1` rather than destructuring — `for (name, data) in ...` does
not parse, because the parser reads the parenthesised pair as the iterable
and reports `no method "iter"` on it. That is a general language gap, not
specific to assets.

Sorted order is deliberate: a filesystem-order walk would make the emitted
program differ between hosts for identical input.

Enumeration puts **every** asset in the binary, whether or not any
`__fern_asset` call names it. That is inherent — reaching assets whose
names the program does not know is the whole point — so a bundle carrying
files the program never serves pays for them in full.

An embed directory holding no files is legitimate: the loop body simply
never runs. The empty array carries a stamped element type, so it does not
demand the type annotation an empty `[]` normally would.

## Why it is nearly free

`__fern_asset("name")` is not a function. It is resolved during const
folding (`internal/constfold`), which replaces the call with a
`StringLit` holding the file's bytes. Everything after that point —
interning, the immortal rc sentinel, all four backends — sees a string
literal and nothing else, so no backend carries asset-specific code and no
container format exists to go wrong.

Three properties of the existing literal layout carry the feature:

- **The explicit byte length at `data-4` makes it NUL-safe.** The `.asciz`
  terminator is never load-bearing, so binary assets (images, fonts, wasm)
  work unchanged.
- **`escapeForGAS` already carries arbitrary bytes** — `\NNN` octal below
  32, high bytes passed through raw rather than re-encoded as UTF-8.
- **The rc sentinel (`0x80000000` at `data-8`) means zero refcount
  traffic.** Assets are immortal, so `__fern_rc_inc/dec` short-circuit on
  them and they can be aliased into containers safely.

Handing an asset to user code is therefore a zero-copy pointer into
demand-paged read-only memory: no parser, no self-reopen, no syscall, no
heap allocation.

Because the substitution happens before the checker, an asset also works
anywhere a string literal works — including in `const` initialisers and in
constant expressions (`__fern_asset("a") + __fern_asset("b")` folds at
compile time).

## Cost: measured, not assumed

Assets reach the native backends as GAS assembly text, which spends 4
characters on each byte below 32 and 1 on most others. Measured on this
repository's own site bundle (HTML, JS, CSS, SVG, WOFF2, PNG — 494 KB):

| Asset kind | Expansion |
| --- | --- |
| Pure ASCII text | 1.00x |
| HTML / CSS / JS | 1.03x – 1.17x |
| Compressed binary (PNG, WOFF2) | 1.37x – 1.51x |
| **Realistic mixed bundle** | **1.31x** |
| All-NUL bytes (worst case) | 4.00x |

#6069 budgeted for ~4x as the *expected* cost and proposed a fourth slice —
splicing raw bytes into a section from the in-process linker — to avoid it.
The measurement says 4x is reachable only by data that is overwhelmingly
low bytes, which is also data that compresses to nearly nothing. **That
slice is therefore not built.** If a future asset bundle does make the asm
text hurt, the linker-splice path remains available precisely because Fern
owns its assembler and linker (`internal/native`).

## Compression

Store HTTP assets **pre-compressed** and serve them with a
`Content-Encoding` header. The binary never decompresses them, so the
runtime needs no decompressor at all.

## Errors

All four are compile-time diagnostics naming the call site:

| Situation | Diagnostic |
| --- | --- |
| No `-embed` passed | `no assets were embedded — pass -embed DIR to the compiler` |
| Unknown name, near miss exists | `no embedded asset "html/index.htm"; did you mean "html/index.html"?` |
| Unknown name, nothing close | `no embedded asset "..."` + the available names |
| Computed name | `needs a string literal — assets are resolved at compile time` |
| `__fern_assets()` given arguments | `takes no arguments, got N` |
| `__fern_assets()` in a `const` | `builds an array, which is not a constant expression — assign it to a \`var\` instead` |

A computed name can never work: there is no later point at which it could
be resolved. Saying so directly beats letting it reach the checker as a
call to an undefined function.

## Scope

Symlinks under the embed root are **skipped, not followed**, so an asset
tree cannot reach outside its root or wedge the walk on a cycle.

What this gives up, deliberately: **late binding** — changing assets in a
shipped binary without recompiling. #6069 considered and rejected
redbean's trailing-ZIP trick, whose whole value is that property; the
reasoning is preserved in the issue. Nothing here forecloses it: appending
a ZIP would be purely additive.

A single asset is a compile-time constant and so is legal in a `const`
initialiser; the **enumeration** is not, because `evalConst` returns scalar
literals and an array of tuples is not one. Bind it with `var`.

## Coverage

| Layer | Tests |
| --- | --- |
| Loading, symlink skip, suggestions, nil set | `internal/embed/embed_test.go` |
| Substitution, const initialisers, const folding, binary bytes, every error path | `internal/constfold/asset_test.go` |
| Enumeration: sorted order, contents, binary bytes, empty bundle, error paths | `internal/constfold/asset_test.go` |
| End-to-end through the native backend + the CLI diagnostics | `cmd/fern/embed_test.go` |
| Enumeration end-to-end + the empty-bundle compile | `cmd/fern/embed_test.go` |

The e2e test's load-bearing assertion is the **binary** asset: its blob
carries interior NULs and bytes >= 0x80, so a correct exit code proves both
that the explicit length (not the `.asciz` terminator) bounds the literal
and that high bytes survive the assembler.
