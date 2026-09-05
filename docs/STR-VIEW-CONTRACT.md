# What enforces `str`'s contract — a design review, and the decision

`STRINGS-SOTA.md` settled the *representation* questions (D1–D11): UTF-8, byte
indices, SSO, the owned/borrowed split itself. This document is about the
question that survey did not ask and 2026-08 answered empirically: **what
enforces the view type's contract**, and the answer today is *nothing* —
safety rests on an accident, and the accident has started failing.

**§5 carries the decision: O2 for `str`, a two-word value for `[u8]`, O3
refused.** §1–§4 are the review that led there, written 2026-08-23 with every
measurement code-verified that day; §5 adds the 2026-09-05 measurements that
settled the `[u8]` half §3 had deferred. The probes in §1 are the tests §3's
step 3 needs; #7393 carries the repro.

## 1. The evidence

### The checker constrains a view's lifetime nowhere

Probed individually against native `fern -check`, all four pass:

- a function **returning** `str` (a view of its own local);
- a `str` **struct field**;
- a `str[]` **array**;
- a view held across its **owner's rebind**.

Fern has no borrow checker, so nothing ties a view to its backing buffer's
lifetime. The one rule that exists — E003, `str` does not assign to `string`
without `.to_owned()` — polices the *other* direction. Both checkers
implement it now: the self-host refuses a view at a var init, an assignment,
a return and a struct field, with native's message text (#7293), and lets one
through in argument position, where a parameter is borrowed rather than
owning (#7086).

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
2. ~~The self-host checker gains E003 and the `str`-where-`string` rule~~
   — done (#7293, #7086).
3. A staged native+self-host checker refusal of `str` in return / field /
   element / capture position — warning first if churn demands, error once
   the corpus is clean. The probes in §1 become its tests.
4. Leak-matrix cells for the view kind (producer, escape shapes), so the
   contract stays measured.

The split itself — `string`/`str` at argument boundaries — stays exactly as
`STRINGS-SOTA.md` §2.2 concluded. What changes is that its contract becomes
something the compiler states, instead of something the leak list implies.

## 5. Decision (2026-09-05)

**O2 is adopted for `str`, and `[u8]` becomes a two-word value rather than a
heap box. O3 is refused for both.** §3's recommendation stands; what follows is
the part §3 left open — it deferred `[u8]` explicitly ("its producers can adopt
whichever contract is chosen here") — plus the evidence that arrived after it
was written.

### The evidence that settles `[u8]`

Measured on `958a003d3`, x86-64, allocations paired by pointer from
`FERN_RC_TRACE=1` on a `-g` build:

```
$ FERN_LEAKCHECK=1 ./c1        # main() { crypto.sha256_hex("abc"); }
leakcheck: allocs=11 frees=8 live_bytes=48

3 unpaired allocation(s):
    16 B  site=__fern_slice_make+0x14   caller=__fn_main+0x1b6
    16 B  site=__fern_slice_make+0x14   caller=__fn_main+0x22c
    16 B  site=__fern_slice_make+0x14   caller=__fn_main+0x22c
```

Every `[u8]` slice leaks its 16-byte `(data, len)` header, always has, and
`rc_caps.go` says why: slices are on the borrow model ("it rejects Map …,
**slices**, closures"), and a borrowed value is never released. #8278 did not
introduce this; it put `[u8]` into a hot stdlib wrapper an audited fixture
calls, which is how the census caught a standing gap (#8534).

**This leak is not the §1 leak floor and buys no safety.** The floor is the
*backing buffer* outliving the view; this is the *descriptor*, which nothing
reads after the call. It is pure waste, so the "views are safe because their
targets leak" trade does not apply to it and nothing is lost by removing it.

The corpus makes the fix cheap. `[u8]` matches 34 times as literal text across
`internal/stdlib` and `examples/self_host`, but 24 of those are outside
comments, and the self-host's single non-comment match is `[u8]` inside an E033
diagnostic *string*, so the self-host carries no `[u8]` annotation at all. The
real corpus is 22 occurrences across 15 distinct stdlib functions, every one of
them taking `[u8]` as a **parameter**.

It is almost only parameter position: never a return type, never a struct
field, never an array element, and one local binding
(`internal/stdlib/std/io_buffered.fern:47`, `var bs: [u8] = s.as_bytes()`).
A local is still non-escaping, so the argument holds — but "only in parameter
position" overstated it.

The representation is **not** already shipped everywhere. The SSO two-word ABI
carries a string as `(data, len)` unboxed on **wasm32 and arm64**; x86-64 is
still single-word LSB-tagged and never sets `ast.TwoWordOverride`
(`SSO-NATIVE-FLIP-STATUS.md`, whose title says so, and
`internal/codegen/x86_64/x86_64.go:2168`). x86-64 is the backend the leak
measurement above ran on, so there the two-word shape is precisely the un-done
flip, not a baseline a slice can inherit: plan that half as new ABI work.

Two things follow that this section originally missed. A two-word string's
`data` word is a tagged value, not always a loadable byte address, so "a slice
is that shape" holds for heap-form strings and needs an answer for inline ones.
And `as_bytes()` on an inline-packed string today "first copies the bytes into
a bare `__fern_alloc` block the header points at; that copy has no owner"
(`internal/ir/rcresults.go:145`, the backends' helpers at e.g.
`internal/codegen/x86_64/x86_64.go:11134`) — an ownerless copy whose only
holder is the header that is being retired. **Open:** does the two-word slice forbid the
inline-materialising path, forcing a heap-form promotion inside the string, or
carry an inline tag of its own? The decision does not turn on the answer; the
implementation does.

### Why not rc-track slices

Making the header rc-tracked would fix the leak and is the wrong fix: it is O3
in miniature, and it buys retention semantics — a stored view silently
extending its buffer's lifetime — for a value that is never stored. It also
adds a count to something whose whole purpose is to be cheaper than the array
it points into. Deleting the allocation dominates reclaiming it.

### Consequences

- The escape story is deferred, not skipped. A two-word `[u8]` cannot dangle
  today because it cannot escape today; §3's step 3 (a staged checker refusal
  of escaping positions) is what keeps that true, and it now covers `[u8]`
  alongside `str` rather than only `str`.
- **The doing is tracked in #8635** — the representation change and the checker
  rule both. This section is a decision, not an owner, and the decision it
  replaced sat unowned for two weeks.
- `__slice_make` and its `rcResultRaw` / `rcsigs` entries retire with the box,
  and so do the other slice-producing classifications an implementer will grep
  for: `__method_string_as_bytes` (`internal/ir/rcresults.go:150`),
  `__slice_range` (`internal/ir/rcsigs.go:377`), and `__slice_make`'s row in
  `internal/ir/verifyprovided.go:248`. A "slice header" allocation surviving
  anywhere afterwards is a bug, which makes the change self-checking.
- #8534's crypto half closes as a consequence rather than as a stdlib rewrite:
  `sha256_hex` was never the defect, and rewriting the wrappers to dodge `[u8]`
  would have hidden a general gap behind one call site.

### What would reopen this

A use for `[u8]` in a return, field or element position. That is a genuine
escape and needs O1's lifetime-dependent returns, not a wider box — the two-word
value is chosen *because* the escape set is empty, and the checker rule is what
holds it empty. If that rule is not landed, this decision decays into C++'s
`string_view` with a cheaper representation, which is worse than today.
