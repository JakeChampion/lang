# Strings and related types — state of the art, and where Fern sits

Companion to `STDLIB-DESIGN-RESEARCH.md` (which surveys HTTP / JSON /
date-time / I/O depth) and `LANGUAGE-DIRECTION.md`. This one surveys the
**string layer**: representation, ownership, indexing, the "character"
question, Unicode operations, and the related types that always ship
alongside (byte views, scalar/char, paths, symbols, builders).

Written because #5552 ("stdlib case ops are ASCII-only — add a Unicode
`std/unicode`") asked a question the codebase can't answer from first
principles yet: *should Unicode be a separate opt-in module, or the
default?* That's not a stdlib-packaging question. It's a question about
what `string` **is** in Fern, and it has to be answered once, explicitly,
before more of the 144-method `std/string` surface accretes against the
wrong default.

Framing note, same as the stdlib-design doc: breaking changes here are
~free today and expensive after the first non-trivial handler ships.
Strings are the type every program touches; a wrong default ossifies
faster than any other.

---

## 1. Where Fern actually is today (code-verified, 2026-07)

Not the aspiration — what's in the tree.

### Representation: already modern

| Property | Status |
|---|---|
| Encoding | UTF-8 **by convention**, not enforced anywhere |
| Layout | Two-word `(data, len)`, top-bit inline flag (`SSO-PLAN.md`) |
| Small-string inline | 15 bytes native, 7 bytes wasm32 — shipped both natives + wasm |
| Heap form | Reference-counted, Perceus (`RC-STRINGS-PLAN.md`) |
| Literals | Interned/deduped; immortal `rc = -1` sentinel |
| Borrowed view | `str` — a real type (`ast.StrType`), erased to `StringType` at the IR choke point (`ir/erase_str.go`) |
| Owned → borrowed | Implicit (`string` → `str` in any argument position) |
| Borrowed → owned | Explicit only (`.to_owned()` copies) |

This is *good*. The representation half of the string problem — UTF-8,
two-word SSO, refcounting, an owned/borrowed split — is where Rust and
Swift landed after years of migration, and Fern already has it. Nothing
below proposes changing it.

### Semantics: pre-Unicode

| Operation | Today |
|---|---|
| `s.len()` | bytes |
| `s[i]` | byte, as `i32` |
| `s[a:b]` | **fresh copy**, no codepoint-boundary check — can split a codepoint |
| `==` | byte equality |
| `.to_upper()` / `.to_lower()` | ASCII byte fold (A–Z ↔ a–z), everything ≥ 0x80 passes through |
| `(b: i32) .to_upper()` / `.is_alpha()` / `.is_digit()` | ASCII byte classes on a bare `i32` |
| `.reverse_bytes()` | bytes — honestly named, produces invalid UTF-8 on multibyte input |
| `.bytes()` | copies into a `u8[]` (a `[u8]` *view* is listed as deferred in `LANGUAGE-DIRECTION.md`) |
| Validity | Never checked. `string_from_bytes` accepts anything |
| `std/utf8` | decode/encode, `codepoints() → i32[]`, `char_at`, `is_valid_utf8`, `substring → str` |
| `std/unicode` | simple (1:1) case mapping + `is_letter`/`is_digit`/… over code points |
| Normalization | none |
| Segmentation (UAX #29) | none |
| Collation | none |
| Case folding | none (`eq_ignore_case` is simple-lowercase, not folding) |
| `std/regex` | no Unicode classes (`\p{…}`), byte-oriented |
| A `char` / scalar type | **does not exist** — code points and bytes are both bare `i32` |

### Three measurements that constrained the design

Taken on this tree, x86-64, `/tmp/fern -target x86-64`. **D7 has since
landed (#5627)**; these are the numbers that set its shape, with the
post-fix figures alongside.

1. **The Unicode tables cost 176 KB of binary.** A program whose only
   Unicode call is `unicode.to_upper(s)`: **184,636 bytes**. The same
   program using the ASCII `s.to_upper()`: **8,492 bytes**. The tables
   were emitted as *code* — `_upper_keys()` was a function that
   materialised a 1450-element `i32[]` literal, ~2900 array stores
   across the upper+lower pair. *Now: 27,820 bytes.*

2. **They cost 22× at runtime, on pure ASCII.** 2000 calls over a
   34-byte ASCII string: `unicode.to_upper` **45 ms**, `s.to_upper()`
   **2 ms**. The tables were rebuilt *per call*, so every call allocated
   ~11.6 KB before touching the string. *Now: 2 ms — parity with the
   byte fold.*

3. **Static data is ~free, and the fix was available today.** A
   12,000-byte *string literal*, indexed 4000× in a loop: binary grows by
   exactly ~12 KB (20,460 vs the 8,492 baseline — 1:1, straight into
   rodata) and the loop runs in ~1 ms including 2000 calls to the
   accessor. Returning a literal from a function does not copy it.

So the shape of the engineering answer was already visible: **tables must
be static data, not constructed arrays** (§5.7), and per-function DCE
already gives Fern the "pay only for what you use" property that ICU4X
calls *data slicing* — the ASCII binary stayed at 8.5 KB.

### One bug found while measuring: the lexer was not UTF-8-aware

*(Fixed in #5628; recorded here because it is what forced the identifier
decision in D11.)*

#5552's body opens with "Fern's lexer is UTF-8-aware". It wasn't — for
identifiers. The scanner applied `unicode.IsLetter` to
`rune(l.src[l.i])`, i.e. to a single **byte** widened to a rune, so
UTF-8 continuation bytes were classified as though they were Latin-1
characters. Observable consequences:

```
var café = 7;   // error: unexpected character '©'   ← mojibake, mid-codepoint offset
var cafê = 7;   // ACCEPTED, prints 7
```

`é` is `C3 A9`; `A9` is `©` in Latin-1, not a letter → rejected, with a
diagnostic pointing at the second byte of a character and naming a
character that isn't in the source. `ê` is `C3 AA`; `AA` is `ª`
(category Lo) → *is* a letter → silently accepted. The accepted set was
"code points all of whose continuation bytes happen to be `ª`, `µ`, or
`º` in Latin-1", which is not a design. See D11 for the fix.

---

## 2. The design space

Nine axes. For each: what the field does, what it has converged on, and
what's still contested.

### 2.1 Storage encoding

| Language | Encoding |
|---|---|
| Rust, Go, Swift (5+), Julia, Zig, Elixir, Haskell `Text` (2.0+), C++ `u8string`, Roc, **Fern** | UTF-8 |
| Java, C#, JavaScript, Kotlin, MoonBit | UTF-16 |
| Python | Flexible (latin-1 / UCS-2 / UCS-4 per string, PEP 393) |
| Raku | NFG — UTF-8-ish with *synthetic* code points for grapheme clusters |

**Converged.** UTF-8 won. The UTF-16 languages are all pre-2005 designs
(or JS-compatibility-bound, which is MoonBit's case) and all of them
carry the surrogate-pair tax forever. Swift *migrated* from UTF-16 to
UTF-8 in Swift 5 — a language-wide ABI break undertaken specifically to
get here. Python's flexible representation buys O(1) code-point indexing
at the cost of three internal representations and a per-string branch on
every access.

Fern is on the winning side of the only axis that's expensive to change.

### 2.2 Ownership and views

| Language | Owned | Borrowed view | Notes |
|---|---|---|---|
| Rust | `String` | `&str` | plus `Cow<str>`, `Box<str>`, `&[u8]`, `bstr` |
| Swift | `String` | `Substring`, **`UTF8Span`** (SE-0464, Swift 6.2, 2025) | `Substring` shares storage; `UTF8Span` is a non-escapable *validated* borrow |
| Go | `string` (immutable) | `string` (slicing is O(1), shares) | `[]byte` conversions copy |
| MoonBit | `String` | `@string.View` (2025) | added specifically to stop exposing UTF-16 offsets |
| C# | `string` | `ReadOnlySpan<char>` | plus `Rune` |
| C++ | `std::string` | `string_view` | |
| **Fern** | `string` | `str` | view type exists, erased at IR |

**Converged, and recently.** Every actively-designed language added a
borrowed string view in the last decade, and the two most recent
additions — Swift's `UTF8Span` (2025) and MoonBit's `View` (2025) — are
both explicitly about letting library code work over *validated* UTF-8
bytes without allocating or copying. Swift's motivation text is worth
quoting because it's precisely Fern's `.bytes()` situation: without it,
"a developer … has to make an instance of String, which allocates …
copies all the bytes, and is reference counted".

Fern has the type. What it lacks is (a) producers that make slicing
non-copying and (b) the `[u8]` byte-view (already listed as deferred).

### 2.3 The indexing model

The genuinely contested axis.

| Model | Languages | Cost |
|---|---|---|
| **Byte offsets** | Rust, Go, Swift (opaque, but byte-backed), Julia, Zig, **Fern** | O(1) index; caller must respect boundaries |
| Code-point offsets | Python, Java/C#/JS (code *units*) | O(1) only via a fat or fixed-width representation; splits graphemes anyway |
| Grapheme offsets | Raku (NFG) | O(1) index, but requires normalizing + synthesizing code points on ingest; non-portable internal form |
| **No indexing at all** | Roc | sidesteps it; forces iterators |

**Not converged, but the trend is clear**: byte offsets plus explicit
iterators. The three positions on what happens at a *bad* index:

- **Rust**: panics on a non-boundary index; provides
  `floor_char_boundary` / `is_char_boundary` to snap safely.
- **Swift**: makes indices opaque (`String.Index`) so a bad one is
  unrepresentable — and then needs "breadcrumbs" (a cached side-table)
  to make UTF-16 offset conversion O(1) for Objective-C interop.
- **Go / Fern**: allows it. Slicing mid-codepoint yields invalid UTF-8
  and nothing complains.

Roc's argument deserves engagement rather than dismissal: getting the
n-th grapheme "is some combination of more error-prone or slower
(usually both) than doing something else that does not require taking
graphemes into consideration". Roc concludes you should *reconsider the
design* and moves code points and graphemes out of the builtins
entirely. That is a defensible position for a language with Roc's
priorities, and it is the strongest argument against putting segmentation
in the default path.

### 2.4 The "character" question — and whether a `char` type exists

| Language | The unit | Type |
|---|---|---|
| Rust | Unicode scalar value | `char` (4 bytes, distinct type) |
| Swift | **Extended grapheme cluster** | `Character`; scalars via `Unicode.Scalar` |
| Go | scalar | `rune` (an *alias* for `int32` — weak) |
| C# | scalar | `Rune` (added .NET Core 3.0, retrofit over `char` = UTF-16 unit) |
| Java | code point | `int` (no type) |
| Python | — | a 1-character `str` (a known wart) |
| JavaScript | — | a 1-code-unit string (a known wart) |
| **Fern** | — | **`i32`** — same type as a byte |

**Converged: a distinct scalar type is correct.** The languages that
didn't do it (Python, JS, Java) all regret it, and C# retrofitted `Rune`
onto a UTF-16 `char` at real cost. Go's `rune` is only an alias, which is
why `for i, r := range s` confuses newcomers about whether `i` counts
bytes or runes.

Fern is currently in the *worst* cell of this table: not only is there no
scalar type, but a byte and a code point are the **same type**, so
`s[i].to_upper()` (a byte, ASCII fold) and `unicode.to_upper_char(cp)`
(a scalar, Unicode mapping) are both `i32 → i32` and neither the checker
nor the reader can tell them apart. That type collision is the root
cause of #5552's confusion, not the missing tables.

Swift is the one language whose default unit is the grapheme cluster.
It's the most *correct* choice and the most expensive: `String` is not
random-access, `count` is O(n), and every index operation walks the UAX
#29 state machine. Swift can afford it; a language whose stated targets
include fast-startup CLI tools and per-request edge handlers should look
hard at that bill before signing.

### 2.5 Case mapping — simple vs full vs folding

Three distinct operations that get conflated:

- **Simple (1:1)** — one scalar in, one out. `ß` stays `ß`.
- **Full (1:N)** — `ß` → `SS`, `ﬁ` → `FI`, final-sigma context rules.
- **Folding** — case-*insensitive comparison*, not display. `ß` folds to
  `ss`; folding is not "lowercase" (`String.casefold` in Python exists
  precisely because `lower()` is the wrong tool for comparison).

Who does what for a *string*-level `to_upper`:

| Language | Behaviour |
|---|---|
| Python, JavaScript, Java | **Full** (`'ß'.upper() == 'SS'`) |
| Go (`strings.ToUpper`) | **Simple** — full mapping lives in `golang.org/x/text/cases` |
| Rust (`str::to_uppercase`) | **Full**; `char::to_uppercase` returns an *iterator* (1→N is in the type) |
| C# (`ToUpperInvariant`) | Simple (1:1 by API contract) |
| Elixir (`String.upcase/2`) | Full, with an explicit **mode** parameter: `:default`, `:ascii`, `:greek`, `:turkic` |
| **Fern `std/unicode`** | Simple |
| **Fern `std/string`** | ASCII only |

**Locale**: nearly everyone defaults to locale-*independent* mapping and
puts Turkish dotless-i behind an explicit opt-in — Elixir's `:turkic`
mode, Go's `unicode.SpecialCase`, Java's `toUpperCase(Locale)`. Java's
choice to make the *default* locale-sensitive is widely considered a
bug-generator (`"TITLE".toLowerCase()` breaks in a Turkish locale).

Rust's `char::to_uppercase → impl Iterator<Item = char>` is the most
honest signature in the survey: it makes 1→N visible in the type instead
of silently dropping the expansion.

### 2.6 Normalization and equality

| Language | `==` | Normalization API |
|---|---|---|
| Swift | **Canonical equivalence** — `"é"` (NFC) `==` `"é"` (NFD) is `true` | implicit; `String` compares under canonical equivalence |
| Raku | canonical, via NFG on ingest | implicit |
| Rust, Go, Python, JS, C#, Java, **Fern** | byte / code-unit equality | explicit (`unicodedata.normalize`, `String.prototype.normalize`, `unicode-normalization` crate, `golang.org/x/text/unicode/norm`) |

**Not converged, and the split is principled.** Swift's canonical `==`
is *the* correct answer for user-facing text and a large ongoing tax:
`==` is no longer a byte compare, hashing must normalize in lockstep, and
`Dictionary<String, _>` pays it on every lookup. Every systems-adjacent
language declined and made normalization an explicit call.

This matters most for exactly the cases #5552 names: search, dedup, and
username/auth comparison. Two visually identical strings comparing
unequal is a real bug — but it's a bug in *specific* code paths, and the
fix can be a call rather than a language-wide equality change.

### 2.7 Segmentation (UAX #29)

| Language | Grapheme/word segmentation |
|---|---|
| Swift | in the core — `Character` **is** a grapheme cluster |
| JavaScript | `Intl.Segmenter` (grapheme/word/sentence) — in the platform, not the language |
| C# | `StringInfo` text elements (updated to UAX #29 in .NET 5) |
| Elixir | `String.graphemes/1` in core |
| Rust | **not in std** — `unicode-segmentation` crate |
| Go | not in std — `x/text` |
| Zig | not in std at all |
| Roc | deliberately moved *out* of builtins |
| **Fern** | absent |

**Not converged**, and the split tracks binary-size sensitivity almost
perfectly: languages with a runtime/platform already loaded (Swift, JS,
.NET, BEAM) put it in core; languages that emit standalone binaries
(Rust, Go, Zig, Roc) keep it out. Fern emits standalone binaries — and
has an explicit binary-size epic (#4109).

### 2.8 Validity — is `string` *guaranteed* well-formed?

| Language | Invariant |
|---|---|
| Rust | **Yes.** `str` is guaranteed UTF-8; construction validates or is `unsafe`. Raw bytes live in `[u8]`/`bstr` |
| Swift | **Yes.** Native storage is guaranteed-valid UTF-8; invalid input is repaired at the boundary. SE-0464 calls the absence of this guarantee a "security and safety" problem |
| Go | **No.** `string` is arbitrary bytes; ops tolerate invalid sequences (`range` yields U+FFFD) |
| JavaScript | No — lone surrogates are representable; ES2024 added `isWellFormed()`/`toWellFormed()` to cope |
| **Fern** | **No**, and nothing checks |

**Converged among the newer designs**: validate once at the boundary,
then every downstream operation can assume well-formedness ("parse,
don't validate"). The counter-consideration is real — Windows paths and
some filesystem APIs are *not* valid Unicode, which is why Rust needs
`OsStr`/WTF-8 and why Go declined the invariant.

Validation is cheap now: `simdutf`-class validators run at multiple GB/s,
and an ASCII-only fast scan is a few instructions per word.

### 2.9 Unicode data — the binary-size problem

The engineering constraint that decides *where* Unicode operations can
live:

- **ICU4X** (Unicode Consortium's Rust library, 2.0 in 2025) is the
  reference answer: data compiled into the binary as static structures,
  with **data slicing** — "static markers … allow the compiler to
  eliminate unused data when data is compiled into the binary". Reported
  footprint 50–90 % smaller than ICU4C.
- **Rust/Go/Zig** keep the tables out of std entirely; you pay by adding
  a dependency.
- **Swift/JS/Java/.NET** get them free from a platform-provided runtime.

Fern's per-function DCE already implements ICU4X's data-slicing property
(measured: §1, the ASCII binary stayed 8.5 KB). What it lacks is the
*static data* half — Fern's tables are executable code today (§1,
measurement 1), which is why they cost 176 KB and 22 µs/call.

### 2.10 Related types that ship alongside

| Type | Who has it | Fern |
|---|---|---|
| Byte string / byte view | Rust `&[u8]`/`bstr`, Go `[]byte`, Swift `UTF8Span`, Python `bytes` | `u8[]` (copy); `[u8]` view **deferred** |
| Scalar / char | Rust `char`, C# `Rune`, Swift `Unicode.Scalar` | **absent** (§2.4) |
| OS string / path | Rust `OsStr`+`Path` (WTF-8), Python `os.PathLike`+surrogateescape, Haskell `OsPath` | `std/path` over plain `string` |
| Interned symbol | compiler-internal everywhere; Ruby `Symbol`, Elixir atoms | `SELFHOST-SYMBOL-INTERNING.md` (compiler-internal) |
| Builder / rope | Go `strings.Builder`, Rust `String::push_str`, Java `StringBuilder`, editors' ropes | **absent** |
| Encoding-tagged string | Ruby | — (rightly) |
| Format/template type | Rust `format_args!`, Python f-strings, JS tagged templates | f-strings |

Two live gaps: the byte view (§2.2) and the scalar type (§2.4). One
open question: a builder — `s = s + chunk` in a loop is O(n²) with
immutable strings unless the compiler special-cases accumulation (the IR
has `rc_str_literal_accum` handling; whether it covers the general case
needs measuring before adding a type).

---

## 3. What the field has converged on

Stripping out the contested parts, the 2026 consensus for a new
statically-typed, standalone-binary language is remarkably tight:

1. UTF-8 storage. (§2.1)
2. Owned string + borrowed view, with the borrow non-allocating. (§2.2)
3. Byte indices, with the boundary hazard made visible rather than hidden. (§2.3)
4. A **distinct scalar type**, separate from both byte and string. (§2.4)
5. Validity guaranteed at the type level, validated once at the boundary. (§2.8)
6. Case/normalization/segmentation are **Unicode-correct by default**,
   with ASCII variants that *say* `ascii` in the name. (§2.5 — Rust's
   `to_uppercase` vs `to_ascii_uppercase` is the naming everyone copies)
7. Byte `==`; canonical equivalence explicit. (§2.6 — Swift dissents)
8. Segmentation opt-in for standalone-binary languages. (§2.7)
9. Unicode data as static tables with dead-data elimination. (§2.9)

Fern has 1 and 2. It is missing 4, 5, 6, 8, 9, and has 3 in its
most-hazardous form.

---

## 4. The question #5552 actually asked

> *Perhaps we make Unicode the default and not a new stdlib?*

**Yes — and the survey is unambiguous.** Point 6 above: no
actively-designed language ships an unqualified `to_upper` that means
ASCII. Rust, Go, Python, JS, Java, C#, Swift, and Elixir all make the
unqualified name Unicode-correct and put `ascii` in the name of the fast
path. C is the only mainstream counter-example, and it needs `locale.h`
to explain itself.

But "make Unicode the default" is not "delete `std/unicode`". The module
is the right *home* for the tables and for normalization/segmentation —
that's exactly what ICU4X-style data slicing (§2.9) is for. The change
is which name is the **default**:

```
                 today                       proposed
  s.to_upper()   ASCII fold                  Unicode, full mapping
  s.to_ascii_upper()  —                      ASCII fold (the fast path, named)
  unicode.to_upper(s)  Unicode, simple       (implementation; module keeps
                                              normalize / segment / fold)
```

The default becomes correct; the fast path stays available and now says
what it is; the module stops being a thing users must *know to reach
for* in order to not have a bug. And per the erasure rule, the old
unqualified ASCII spelling is **deleted**, not deprecated.

---

## 5. Decisions for Fern

### D1 — Keep UTF-8 + byte indices. No representation change.

Fern is already on the converged side of §2.1/§2.2. Explicitly reject:
Python's flexible representation, Raku's NFG, and Swift's
grapheme-default `Character`. Rationale: all three buy correctness or
O(1) grapheme indexing with a permanent per-operation tax that a
fast-startup / per-request language shouldn't sign up for.

### D2 — Add a `char` type: the Unicode scalar value.

The highest-leverage change in this document, and independent of every
table. Today a byte and a code point are both `i32` (§2.4), which is why
`.to_upper()` means two different things in two places with identical
types.

- `char` = a Unicode scalar value (0..0x10FFFF minus surrogates),
  stored in an i32 slot, distinct in the checker.
- `u8` stays the byte type; `s[i]` yields `u8` (today: `i32`).
- `std/utf8`'s `codepoints() → i32[]` becomes `char[]`;
  `unicode.to_upper_char(cp: i32)` becomes `char.to_upper()`.
- Deletes: the `(b: i32) to_upper/is_alpha/is_digit` byte methods move
  to `u8` as `to_ascii_upper` / `is_ascii_alpha` / `is_ascii_digit`.

This makes the ASCII/Unicode distinction a **type** distinction rather
than a naming convention, which is the only version of it that survives
contact with a 144-method stdlib.

### D3 — Unicode by default; `ascii` in the name of the fast path. — **LANDED for `to_upper`/`to_lower` (#5630)**

Answers §4. Specifically:

| Was | Becomes |
|---|---|
| `s.to_upper()` (ASCII) | `s.to_ascii_upper()` |
| — | `s.to_upper()` (Unicode, full mapping, ASCII fast path inside) |
| `(b: i32) b.to_upper()` | `(b: u8) b.to_ascii_upper()` |
| `s.eq_ignore_ascii_case(o)` | keep (already honest) — **kept** |
| — | `s.eq_ignore_case(o)` (Unicode, case-*folded* — §2.5) — **LANDED** |
| `s.is_alpha_only()` (ASCII) | `s.is_ascii_alpha_only()`; Unicode via `char.is_alphabetic()` |

Implementation note, load-bearing — and this is how it was resolved.
`s.to_upper()` was **not** just a stdlib function: the self-host
compiler intercepted the method name in `irlower.fern` and lowered it to
`op_str_to_upper` → the `__fern_str_to_upper` runtime helper, while the
native compiler resolved it to `std/string`'s `__string_case_fold`. Two
implementations of one name, which would have silently diverged the
moment one side became Unicode.

The fix was **not** to delete the interception (the issue's first guess)
but to *rename what it intercepts*: the runtime helper implements a byte
fold, so it is now reached by `to_ascii_upper` / `to_ascii_lower`. That
frees `to_upper` / `to_lower` to fall through to ordinary method
resolution and land on `std/string`, which delegates to `std/unicode`.
One implementation per name, the fast path keeps its inline op, and
nothing was deleted. `std/string` → `std/unicode` → `std/utf8` →
`std/string` is a genuine import cycle; modload's load-once dedupe
resolves it, and the self-host loader compiles the whole chain
(`TestSelfHostStdlibFuncsIR`).

`swap_case`, `capitalize` and `title_case` followed in the same shape:
Unicode under the plain name, byte fold as `to_ascii_swap_case` /
`to_ascii_capitalize` / `to_ascii_title_case`. The Unicode `title_case`
also breaks words on any Unicode whitespace, where the byte version
breaks on U+0020 alone.

Still ASCII, pending the rest of D3: the `is_*_only` predicate family
and the `eq_ignore_case` row above (which needs case *folding*, so it
belongs with D4's tables).

### D4 — Full (1→N) case mapping for strings; simple for `char`; locale-independent. — **LANDED, except Final_Sigma (#5630)**

`"ß".to_upper() == "SS"`. This is Python/JS/Java/Rust behaviour and it's
what "correct by default" means; simple mapping (Go, C#, today's
`std/unicode`) leaves a visible wrong answer on the most-cited example
in the issue.

- `string.to_upper` / `to_lower` — **full**.
- `char.to_upper` — **simple**, returning a `char` (a 1→N expansion has
  no `char` to return). Rust's iterator-returning signature is more
  honest; a `char.to_upper_full(): string` is the escape hatch if
  someone needs it. Do not silently drop the expansion without saying so
  in the doc comment.
- **Locale-independent.** No Turkish tailoring in the default path. If a
  user ever needs it, copy Elixir's shape — an explicit mode argument
  (`:default` / `:ascii` / `:turkic`) — not Java's implicit default
  locale, which is a documented bug-generator (§2.5).

### D5 — `==` stays byte equality. Canonical equivalence is a call.

Reject Swift's canonical `==` (§2.6). Rationale: `==` must stay a byte
compare so it stays O(n) and so hashing/`Map` keys don't need to
normalize in lockstep — Fern's `Map[string, V]` is on the hot path of
every handler. Instead:

- `unicode.normalize(s, NFC | NFD | NFKC | NFKD)`
- `unicode.eq_canonical(a, b)` — normalize-and-compare
- Document, in `std/string`'s header, that `==` is byte equality and
  which situations (search, dedup, usernames — the issue's own list)
  need `normalize` first.

Normalization is the part of #5552 with the highest correctness payoff
per byte of table, and it's the part currently at zero.

### D6 — Segmentation stays opt-in. `len()` stays bytes.

Follow Rust/Go/Zig/Roc, not Swift (§2.7) — Fern emits standalone
binaries and has a binary-size epic. Concretely:

- `s.len()` — bytes. Unchanged.
- `utf8.codepoint_count(s)` — exists. Unchanged.
- `unicode.graphemes(s) → str[]` / `unicode.grapheme_count(s)` — new,
  opt-in, tables DCE'd unless called.
- `reverse_bytes` keeps its name (it is already the honest one);
  add `unicode.reverse_graphemes(s)`.
- Take Roc's advice as *documentation*: the `graphemes` doc comment
  should say that wanting the n-th grapheme is usually a design smell
  and name the alternative (iterate, or use a segmentation-aware
  operation directly).

### D7 — Unicode tables become static data. This is a prerequisite, not a follow-up. — **LANDED (#5627)**

Measured in §1: tables-as-code cost 176 KB and 22 µs/call; the same
bytes as a string literal cost 12 KB and ~0. Until this changed, "Unicode
by default" would have meant every `to_upper` caller paying 176 KB and
22×.

Both steps below shipped. Result: **176 KB → 27.8 KB** and **22× → 1×**
(parity with the raw byte fold on ASCII), with 351 case runs replacing
2900 key/value pairs, verified against Go's `unicode` for every code
point in 0..MaxRune at generation time *and* on every `go test`.

Two steps, both available today:

1. **Range-coalesce the case data.** The current table is 2900
   individual (key, value) pairs. Case mappings are overwhelmingly
   *runs* — contiguous blocks with a constant delta, plus the
   alternating even-upper/odd-lower pattern that fills Latin Extended-A.
   Go's `unicode.CaseRanges` encodes all of Unicode's simple case
   mapping in ~a few hundred entries using exactly this shape. Regenerate
   from `cmd/unicodegen` by *deriving* runs from actual `ToUpper`/
   `ToLower` results (the existing `coalesce` helper already does this
   for predicates), so correctness doesn't depend on trusting a copied
   table.
2. **Emit tables as string literals, not `i32[]` literals.** Fixed-width
   ASCII-safe encoding (e.g. 4 chars per 24-bit field over a 64-char
   alphabet — avoiding `"` and `\`), binary-searched with byte loads.
   Literals go to rodata, are interned, and cost nothing per call.

Estimated result: full simple-case data in single-digit KB, with the
per-call table construction gone entirely. The *language* fix is
const/static arrays (see `COMPTIME-BRIEF.md`); the string-literal
encoding is the version that ships without a language change, and it can
be deleted when const arrays land.

Plus an **ASCII fast path** inside every Unicode entry point: scan for
any byte ≥ 0x80 and delegate to the byte fold when there is none. That's
what keeps D3 from regressing the CLI/header workloads the constraints
section of #5552 (rightly) protects.

### D8 — Add the `[u8]` view over strings; stop copying in `.bytes()`.

Already listed as deferred in `LANGUAGE-DIRECTION.md`. This is Fern's
`UTF8Span`/`&[u8]` (§2.2), and every parser in the tree — `std/json`,
`std/csv`, `std/regex`, the self-host lexer — currently pays a full copy
through `.bytes()` to do byte-level work.

### D9 — Guarantee UTF-8 validity on `string`. (Biggest change; sequence it last.)

The §2.8 invariant. `string` means *well-formed UTF-8*; arbitrary bytes
live in `u8[]`/`[u8]` (which D8 makes ergonomic). Validation happens at
a small, enumerable set of boundaries: `string_from_bytes`, file/socket
reads, env/args, FFI returns.

Costs, stated honestly:

- **`s[a:b]` must stop being able to split a code point.** Today it
  can. Options: error (Rust panics), snap to the nearest boundary
  (`floor_char_boundary`), or return `Option[str]`. Recommend
  `Option[str]` — Fern already prefers `Option` over trapping, and the
  hazard becomes visible in the type.
- Ingest paths pay a validation scan. Cheap (ASCII fast scan; §2.8), but
  not free, and it lands on exactly the request path the language cares
  about — measure it before committing.
- Non-UTF-8 filesystem paths become unrepresentable. See D10.

The payoff: `std/utf8`'s `is_valid_utf8` stops being something callers
must remember, `.chars()` can skip validity checks, and the "é is a
hazard" framing in #5552 becomes "é is data".

### D10 — No `OsStr`. Document the UTF-8-path assumption instead.

Rust needs `OsStr`/WTF-8 because Windows paths are UTF-16 with lone
surrogates and Unix paths are arbitrary bytes. Fern's targets are Linux,
macOS and WASI. The honest position is: **Fern assumes paths are valid
UTF-8**, that this is a deliberate simplification, and that a path
containing invalid UTF-8 is a boundary error rather than a value. Write
it down in `std/path` rather than leaving it implied — the same honesty
`FLOAT-SEMANTICS.md` / `INTEGER-SEMANTICS.md` apply to their domains.

### D11 — Fix the lexer, and decide identifiers explicitly. — **LANDED (#5628)**

**Decided: identifiers are ASCII-only.** The lexer now classifies with
explicit ASCII predicates instead of handing raw bytes to
`unicode.IsLetter`, and reports the real character:

```
var café = 7;   // error: identifiers must be ASCII; found 'é'
var cafê = 7;   // same error — it no longer compiles
x — 1           // error: unexpected character '—'
var x = \xff;   // error: invalid UTF-8 byte 0xFF
```

Non-ASCII stays free in string literals and comments. The caret lands on
the first byte of the offending character rather than a continuation
byte. `unicode.IsSpace` on a raw byte also went — it matched 0x85 and
0xA0, which are continuation bytes, not spaces.


§1's bug. Whatever the policy, the current behaviour — Latin-1
predicates applied to UTF-8 continuation bytes, so `cafê` compiles and
`café` doesn't — is a bug.

Recommendation: **ASCII-only identifiers**, decoded properly, with a
clear diagnostic ("identifiers must be ASCII; found `é`") that points at
the *character* rather than a continuation byte. Rationale: it's what
Fern already (accidentally) almost does, it's trivially specifiable, and
it dodges the confusable/bidi security surface (UTS #39, UAX #31) that
Rust had to write an RFC and a lint for. If Unicode identifiers are
wanted later, the upgrade path is UAX #31 `XID_Start`/`XID_Continue`
plus a confusables lint — additive, and a decision worth making on
purpose rather than by byte-classification accident.

Either way the lexer must decode UTF-8 properly to produce the
diagnostic, so the fix is the same work.

### Non-goals

- **Collation** (UCA / locale-aware sorting). Enormous data, needs
  locale, and `sort` on strings by byte order is the right default for
  CLI/handler work. If it ever lands it's a separate package.
- **Encoding conversion** (latin-1, Shift-JIS, …). UTF-8 in, UTF-8 out.
- **Encoding-tagged strings** (Ruby). Universally regretted.
- **Bidi / shaping / rendering.** Not a language concern.
- **Locale-sensitive case by default** (Java's mistake). D4.

---

## 6. Sequencing

Ordered by (payoff ÷ blast radius), and by what unblocks what.

Tracked as epic #5626; issue numbers below.

| # | Slice | Depends on | Notes |
|---|---|---|---|
| 1 | **D7** (#5627) — static tables + range coalescing + ASCII fast path — **DONE** | — | Pure win; made `std/unicode` usable at all (176 KB → 27.8 KB, 22× → parity). Prerequisite for D3. |
| 2 | **D11** (#5628) — lexer UTF-8 fix + identifier policy — **DONE** | — | Self-contained bug fix; correct diagnostics. Identifiers are ASCII-only. |
| 3 | **D2** (#5629) — the `char` type | — | Checker + `std/utf8` + `std/unicode` signatures. Big but mechanical; unblocks honest naming everywhere. |
| 4 | **D3 + D4** (#5630) — flip the default, full case mapping | 1, 3 | Touches the self-host builtin (`irlower.fern` / `asmcore.fern`) **and** the native stdlib — see D3's implementation note. Differential coverage required. |
| 5 | **D5** (#5631) — normalization + `eq_canonical` | 1 | Highest correctness payoff of the remaining Unicode work; new tables, same static-data machinery as slice 1. |
| 6 | **D8** (#5632) — `[u8]` string view | — | Independent; unblocks copy-free parsers. |
| 7 | **D6** (#5633) — grapheme segmentation | 1, 3 | Largest table; opt-in. |
| 8 | **D9** (#5634) — the UTF-8 validity invariant | 6 | Largest blast radius; do last, after `[u8]` makes "raw bytes" ergonomic. |
| 9 | **D10** (#5635) — document the path assumption | — | Doc-only, any time. |

#5552 as filed maps onto slices 1, 4, 5, 6, 7. Its step 1 (document the
ASCII contract) is already done (#5620).

---

## Sources

- [SE-0464: UTF8Span — Safe UTF-8 Processing Over Contiguous Bytes](https://github.com/swiftlang/swift-evolution/blob/main/proposals/0464-utf8span-safe-utf8-processing.md)
- [UTF-8 String — Swift.org](https://www.swift.org/blog/utf8-string/)
- [ICU4X 2.0 released — The Unicode Blog](http://blog.unicode.org/2025/05/icu4x-20-released.html)
- [ICU4X data management tutorial](https://icu4x.unicode.org/2_0/tutorials/data-management/)
- [Roc — Str builtins](https://www.roc-lang.org/builtins/Str) and [Roc FAQ](https://www.roc-lang.org/faq)
- [MoonBit updates 2025-02-10](https://www.moonbitlang.com/updates/2025/02/10/index) / [2025-03-24](https://www.moonbitlang.com/updates/2025/03/24/index) (UTF-16 storage, `@string.View`)
- Fern, in-tree: `docs/SSO-PLAN.md`, `docs/RC-STRINGS-PLAN.md`,
  `docs/LANGUAGE-DIRECTION.md`, `docs/STDLIB-ROADMAP.md` §17,
  `internal/stdlib/std/{string,utf8,unicode}.fern`,
  `cmd/unicodegen/main.go`, `internal/lexer/lexer.go`,
  `examples/self_host/{irlower,asmcore}.fern`
