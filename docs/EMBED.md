# Asset embedding

Status: [reference] shipped. Issue
[#6069](https://github.com/JakeChampion/lang/issues/6069), sub-issue (b) of
the single-file-distributable epic
[#6067](https://github.com/JakeChampion/lang/issues/6067).

Fern embeds assets into the binary at **compile time**. A directory is
handed to the compiler; each use of a file becomes an ordinary literal in
the program.

```
fern -embed ./assets -target x86-64-linux -o prog prog.fern
```

The Go oracle (`cmd/fern`) takes the flag too, for its own `-check` and
`-interp`, and passes it on to the compiler for a `-target` build.
`fern-selfhost`, the compiler without the stdlib embedded, spells it
identically and takes the stdlib root as its trailing positional argument:

```
fern-selfhost -embed ./assets -target x86-64-linux -o prog prog.fern internal/stdlib
```

```fern
const PAGE: string = __fern_asset("html/index.html");

function main(): i32 {
    print(PAGE);
    let icon: u8[] = __fern_asset_bytes("img/favicon.png");
    return 0;
}
```

Names are slash-separated paths **relative to the embed root**, on every
host OS: `-embed ./assets` publishes `assets/html/index.html` as
`"html/index.html"`.

## Text and bytes

Every `string` is valid UTF-8 and arbitrary bytes live in `u8[]` (D9 in
`docs/STRINGS-SOTA.md`), so the builtins split the way `read_file` and
`read_file_bytes` do:

| Builtin | Yields | A file that is not valid UTF-8 |
| --- | --- | --- |
| `__fern_asset("NAME")` | `string` | compile error naming the file |
| `__fern_asset_bytes("NAME")` | `u8[]` | embedded as is |
| `__fern_assets()` | `(string, string)[]` | compile error naming the file |
| `__fern_assets_bytes()` | `(string, u8[])[]` | embedded as is |

The text form checks the file when the program is compiled rather than when
it runs: the file cannot change after that, so a runtime check could only
ever give the answer the compiler already had. HTML, CSS, JS, SVG and a
module's source are text; an image, a font, a wasm module or a compressed
file is bytes.

A file's **name** is always a string, so a file whose name is not valid
UTF-8 makes the directory unembeddable: `-embed` reports it and nothing
compiles.

## Enumeration

`__fern_assets()` and `__fern_assets_bytes()` yield every embedded asset as a
`(name, contents)` tuple, in sorted-name order:

```fern
function main(): i32 {
    for (name, data) in __fern_assets_bytes() {
        print(name);
        serve(name, data);   // data is u8[]
    }
    return 0;
}
```

A bundle that mixes text and binary files enumerates with
`__fern_assets_bytes()`; `__fern_assets()` refuses it, because one of its
strings could not be valid.

Sorted order is deliberate: a filesystem-order walk would make the emitted
program differ between hosts for identical input.

Enumeration puts **every** asset in the binary, whether or not any
single-asset call names it. That is inherent — reaching assets whose
names the program does not know is the whole point — so a bundle carrying
files the program never serves pays for them in full.

An embed directory holding no files is legitimate: the loop body simply
never runs. The empty array carries a stamped element type, so it does not
demand the type annotation an empty `[]` normally would.

## Why it is free at run time

No asset builtin is a function. Each is resolved as the program loads — by
`internal/check/constfold` on the Go side and by `compiler/embed.fern` in
the compiler — which replaces the call with a literal: a text form with a
string literal, a bytes form with a `u8[]` literal of `u8` elements.
Everything after that point sees a literal and nothing else, so no backend
carries asset-specific code and no container format exists to go wrong.

Both literals are **immortal static boxes**: a string literal's box, and
the packed byte box a constant `u8[]` literal folds to (`ir.op_const_bytes`,
a byte an element). Their rc word is the immortal sentinel, so
`__fern_rc_inc/dec` short-circuit on them and they can be aliased into
containers safely; a byte box's capacity is 0, so growing or rewriting one
copies it rather than writing through. Handing an asset to user code is
therefore a pointer into the binary's data: no parser, no self-reopen, no
syscall, no heap allocation.

Because the substitution happens before the checker, an asset also works
anywhere a literal of its type works — including in `const` initialisers and,
for the text form, in constant expressions (`__fern_asset("a") +
__fern_asset("b")` folds at compile time).

## Cost: measured, not assumed

**In the binary**, an asset costs its own size: the 1 MB bytes asset below
adds 1,000,000 bytes to the x86-64 executable, 1,002,824 to the wasm module.
A text asset is a string literal, which the assemblers carry at close to one
character per byte.

**In the compiler**, the two forms differ. A text asset is one literal node
whatever its size. A bytes asset is a `u8[]` literal with one element per
byte, and each element is a value through the semantic lowering until the
constant folds back into one packed box, so compile time and memory are
linear in the asset's size. Measured on a 4-core container, compiling a
program that reads one random-byte asset:

| Asset | Target | Compile | Peak memory |
| --- | --- | --- | --- |
| 100 KB | x86-64 | 0.7 s | 180 MB |
| 1 MB | x86-64 | 8.7 s | 1.66 GB |
| 1 MB | arm64 | 7.1 s | 1.65 GB |
| 1 MB | wasm | 7.5 s | 1.67 GB |

The x86-64 assembly text for the 1 MB asset is 4.6 MB: a packed byte box is
written as `.byte` lists of decimal values. That stays inside the compiler,
which assembles it itself.

A text asset should therefore stay a text asset; the bytes form is for files
that are not text. `TestSelfHostEmbedBytesCompileLinear` holds the bytes form
to linear time: before it, one lookup in the RC lowering scanned a whole block
per instruction, and a 100 KB asset took 61 s.

## Compression

Store HTTP assets **pre-compressed** and serve them with a
`Content-Encoding` header. The binary never decompresses them, so the
runtime needs no decompressor at all.

## Errors

Each is a compile-time diagnostic naming the call site:

| Situation | Diagnostic |
| --- | --- |
| No `-embed` passed | `no assets were embedded — pass -embed DIR to the compiler` |
| Unknown name, near miss exists | `no embedded asset "html/index.htm"; did you mean "html/index.html"?` |
| Unknown name, nothing close | `no embedded asset "..."` + the available names |
| Computed name | `needs a string literal — assets are resolved at compile time` |
| An enumeration given arguments | `takes no arguments, got N` |
| A text form on a file that is not UTF-8 | `__fern_asset: embedded asset "img/favicon.png" under DIR is not valid UTF-8 — read it as u8[] with __fern_asset_bytes` |
| A bytes form under an operator in a `const` | `__fern_asset_bytes() builds an array, which an operator cannot take in a constant expression` |

A file whose name is not UTF-8 is refused by `-embed` itself, before any
call site is read: `-embed DIR: file name "bad\xff.txt" is not valid UTF-8`.

A computed name can never work: there is no later point at which it could
be resolved. Saying so directly beats letting it reach the checker as a
call to an undefined function.

## Scope

Symlinks **under** the embed root are skipped, not followed, so an asset
tree cannot reach outside its root or wedge the walk on a cycle. So are
sockets, FIFOs and device nodes: the rule is "a regular file or a directory,
or nothing".

The **root** is followed, because it is the path named on the command line
and `-embed ./link-to-assets` plainly means the directory behind it.

The compiler and the oracle draw the line in the same place. The oracle uses
`os.Stat` on the root and `WalkDir`'s per-entry `lstat` below it; the compiler
uses `stat` on the root and the `lstat` builtin below it. `lstat` exists for this (#7982) —
`stat` follows links and `FileStat` carries no link bit, so before it the
self-host had no way to ask the question at all. Under `lstat` a symlink is
neither `is_file` nor `is_dir`, which is the same three-way answer
`fs.DirEntry.Type()` gives a walk.

What this gives up, deliberately: **late binding** — changing assets in a
shipped binary without recompiling. #6069 considered and rejected
redbean's trailing-ZIP trick, whose whole value is that property; the
reasoning is preserved in the issue. Nothing here forecloses it: appending
a ZIP would be purely additive.

Every form is a compile-time constant, so any may initialise a `const`:
`const ICON: u8[] = __fern_asset_bytes("img/favicon.png");` and
`const XS: (string, u8[])[] = __fern_assets_bytes();` fold to the same value
a `let` would hold, in the compiler and the oracle.

## Coverage

| Layer | Tests |
| --- | --- |
| Loading, symlink skip, a non-UTF-8 name, suggestions, nil set | `internal/pkg/embed/embed_test.go` |
| Substitution of every form, const initialisers, const folding, binary bytes, the text forms refusing a non-UTF-8 file, every error path | `internal/check/constfold/asset_test.go` |
| Enumeration: sorted order, contents, binary bytes, empty bundle, error paths | `internal/check/constfold/asset_test.go` |
| The CLI diagnostics, the text form's refusal among them | `cmd/fern/embed_test.go` |
| The self-host bundle + substitution, every error path | `compiler/drivers/embed_run.fern`, gated by `internal/testing/e2ecompiler/self_host_embed_test.go` |
| The oracle, its interpreter and the self-host agreeing on the same source + the same directory, symlinked entries, a symlinked root and a non-UTF-8 name included | `TestSelfHostEmbedMatchesNative` |
| A non-UTF-8 asset read back byte-exact through every bytes form on x86-64, arm64 and wasm, under the sanitizer and the leak census | `TestSelfHostEmbedBytesEveryTarget` |
| A symlinked root, followed | `internal/pkg/embed/embed_test.go` |
| `lstat` itself | `internal/testing/e2e/lstat_native_test.go`, `internal/testing/e2ecompiler/self_host_lstat_test.go` |

The e2e test's load-bearing assertion is the **binary** asset: its blob
carries an interior NUL, a lone 0xff, an overlong encoding, a surrogate and a
lead byte past U+10FFFF, so a correct exit code on all three targets proves
the bytes reach the program unchanged — and the same program growing and
rewriting its copy proves the shared static box is never written through.
