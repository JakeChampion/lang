# What enforces `str`'s contract — a design review

`STRINGS-SOTA.md` settled the *representation* questions (D1–D11): UTF-8, byte
indices, SSO, the owned/borrowed split itself. This document is about the
question that survey did not ask and 2026-08 answered empirically: **what
enforces the view type's contract**, and the answer today is *nothing* —
safety rests on an accident, and the accident has started failing.

Written 2026-08-23, every measurement code-verified that day. The probes live
in the discussion below rather than a suite because the conclusion is a
decision request, not a regression pin; #7393 carries the repro that should
become a test with whichever option is chosen.

## 1. The evidence

### The checker constrains a view's lifetime nowhere

Probed individually against native `fern -check`, all four pass:

- a function **returning** `str` (a view of its own local);
- a `str` **struct field**;
- a `str[]` **array**;
- a view held across its **owner's rebind**.

Fern has no borrow checker, so nothing ties a view to its backing buffer's
lifetime. The one rule that exists — E003, `str` does not assign to `string`
without `.to_owned()` — polices the *other* direction, and the self-host
checker does not implement even that (#7293, #7086).

### Three toolchains implement three different `.trim()`s

The same one-call program (#7393): build a heap string in a producer, return
`s.trim()`, read a byte of the view in the caller.

| toolchain | mechanism | result |
|---|---|---|
| interp | (oracle) | correct |
| native x86-64 | `.trim()` **copies** — never creates the aliasing view | correct, balanced census |
| self-host x86-64 | zero-copy view; the producer's sweep frees the viewed buffer | **wrong answers** — reads 0 from the freed block, or the recycler's bytes |
| self-host wasm | zero-copy view; nothing recycles the same way | correct on this probe — the silent variant |

Native is safe *by not implementing the view*. The self-host implements the
documented semantics and dangles deterministically — no diagnostic, underflow
counter 0, leakcheck clean but for the known 24-byte view-box floor. Every
memory instrument reads clean while the program computes wrong answers, the
census-blindness class the #7253 thread catalogued, in its worst form.

`split` has the same fault (#7230, filed independently); `std/utf8.substring
→ str` is the third producer of the class and has not been probed.

### The safety that does exist is the leak floor — and it is being fixed

A *viewed* string loses its reclaim credit to the escape gates, so in most
shapes the backing buffer simply never frees: views are safe because their
targets leak. That is the same latent structure the #7253 thread names for
name-keyed credits — **"every remaining gap is a trap armed by its own future
fix"** — one level up: goal 2's whole purpose is to close exactly these
leaks, and each closure converts a masked view into #7393. The rc work and
the view semantics are on a collision course by construction.

## 2. What other languages enforce with

| Language | View | Enforcement |
|---|---|---|
| Rust | `&str` | lifetimes — the full borrow checker |
| Swift 6.2 | `UTF8Span` | **non-escapable type** (`~Escapable`) + lifetime-dependent returns — no borrow checker required of the user |
| Go, MoonBit | slices / `View` | GC — a live view keeps the buffer alive |
| C++ | `string_view` | **nothing** — the famous dangling-view footgun |
| Fern today | `str` | nothing (checker) + the leak floor (rc, eroding) |

Fern is currently C++'s `string_view` with reference counting underneath —
the one design every survey warns about. The two models available to a
language without lifetimes are Swift's (make escape unrepresentable) and
Go's (make the view an owner). Rust's is not on the table.

## 3. The options

**O1 — non-escapable `str`.** A checker rule set: `str` is legal as a
parameter type and a local binding; illegal as a return type, struct/tuple
field, array element, capture, or global. This is Swift's `~Escapable`
without the general machinery. It kills the #7230/#7393 class in the
checker. Cost: `trim`/`split`/`substring` *return* views, so their current
signatures become illegal — they need either owned returns (collapsing into
O2 for producers) or lifetime-dependent returns tied to the receiver, which
is a real language feature to design, not a rule.

**O2 — view producers copy; views live only at call boundaries.** Align the
self-host `.trim()`/`.split()` with what native already does (copy), and keep
`str` for the purpose it demonstrably serves today: the implicit `string →
str` coercion at argument positions, which is non-escaping *by construction*.
Escaping `str` positions (returns, fields, elements) get a staged checker
refusal. Cost: zero-copy trim/split is given up until a real non-escapable
design exists; the D8 `[u8]` byte-view is unaffected (its producers can adopt
whichever contract is chosen here).

**O3 — views retain the buffer.** A view box holds `+1` on its backing
buffer; the view's release decs it. Zero-copy survives and dangling is
impossible. Cost: the immortal negative-rc view-box design is redone; every
view producer and `__fern_str_view_free` change together; and a stored view
silently extends its buffer's lifetime — Go's semantics, with rc instead of
GC, including Go's "small view pins large buffer" retention hazard.

## 4. Recommendation

**O2 now, O1 as the destination.** O2 is the smallest sound state: it is
already native's de-facto behaviour, so it is a parity fix (#7230, #7393) plus
a checker rule, not a semantics change; it removes the rc/view collision
course entirely; and it costs performance only at call sites that were
wrong on half the toolchain anyway. O1's non-escapable type is the right
long-term shape for a no-lifetimes language — Swift has proven the model —
but its return-position story is a design (lifetime-dependent returns), and
`docs/OWNERSHIP-INFERENCE-PLAN.md` is where that thinking already lives.
O3 buys zero-copy at the price of redesigning the view runtime *and*
adopting retention semantics nobody asked for.

Concretely, in order:

1. Self-host `.trim()` / `.split()` (and audit `substring`) return owned
   copies — fixes #7230 and #7393 as parity bugs. The differential suites
   gate it; the leak-matrix's view rows flip with it.
2. The self-host checker gains E003 and the `str`-where-`string` rule
   (#7293, #7086 — already filed, now load-bearing).
3. A staged native+self-host checker refusal of `str` in return / field /
   element / capture position — warning first if churn demands, error once
   the corpus is clean. The probes in §1 become its tests.
4. Leak-matrix cells for the view kind (producer, escape shapes), so the
   contract stays measured.

The split itself — `string`/`str` at argument boundaries — stays exactly as
`STRINGS-SOTA.md` §2.2 concluded. What changes is that its contract becomes
something the compiler states, instead of something the leak list implies.
