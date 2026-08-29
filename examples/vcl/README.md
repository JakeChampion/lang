# A VCL front end and evaluator, in Fern

A lexer, parser, syntax tree, printer, **and evaluator** for the **Varnish
Configuration Language** (VCL 4.0 / 4.1) — the language Varnish Cache users
write to express caching policy. Written in Fern, depending on nothing
outside `std`.

```
$ fern -interp vclfmt.fern -- testdata/sample.vcl          # parse + reformat
$ fern -interp vclrun.fern -- testdata/policy.vcl GET /static/app.css -n 2
--- request 1 ---
  path:        vcl_recv -> vcl_hash -> vcl_miss -> vcl_backend_fetch -> vcl_backend_response -> vcl_deliver
  disposition: deliver (miss)
  status:      200 OK
  log:
    fetch origin -> 200
    store /static/app.css| ttl=3600.000
--- request 2 ---
  path:        vcl_recv -> vcl_hash -> vcl_hit -> vcl_deliver
  disposition: deliver (HIT)
```

Two drivers: `vclfmt` parses and reprints, `vclrun` executes a request
against a policy. Both exit 0 on success, 1 on a VCL error, 2 when the
file cannot be read or parsed.

## Layout

| File | What it is |
|---|---|
| `ast.fern` | The tree. One module, because the node types are mutually recursive. |
| `lexer.fern` | Tokenizer: identifiers, three string forms, unit suffixes, comments, inline C. |
| `parser.fern` | Recursive descent, vcc's grammar, per-statement error recovery. |
| `printer.fern` | Tree back to source. |
| `value.fern` | VCL's types and its implicit coercion rules. |
| `runtime.fern` | The five HTTP messages a transaction reads and writes, plus the cache. |
| `vars.fern` | The variable namespace and its per-subroutine scoping. |
| `eval.fern` | Expression evaluation, statement execution, ACL and regex matching. |
| `machine.fern` | The request state machine and the built-in subroutines. |
| `vclfmt.fern` / `vclrun.fern` | The drivers. |
| `vcl_test.fern` / `vclbackend_test.fern` | 41 + 38 TAP cases. |

## The front end

Declarations: `vcl 4.1;`, `import` in both forms, `include`, `backend`
(including 4.1's `backend name none;`), `probe`, inline probes as
block-valued properties, `acl` with all four entry forms, `sub`.

Statements: `set` with all five assignment operators, `unset`,
`if`/`elseif`/`elsif`/`else if`/`else`, every `return` form, `call`, `new`,
bare calls, `include`, `C{ … }C`.

Expressions follow **vcc's grammar**, whose one real surprise is that `!`
binds *looser* than the comparison operators — so `!client.ip ~ purgers`,
the idiomatic ACL-mismatch test, means `!(client.ip ~ purgers)` and not
`(!client.ip) ~ purgers`. Reading it the C way inverts the sense of every
ACL check written that way.

The tree is deliberately concrete: it keeps written parentheses, the
`elseif` spelling each arm used, and which delimiters a string was written
with, so `parse` → `print` is a fixed point.

## The evaluator

Of the three plausible back ends, this is the one that could be built and
**tested** in-repo:

1. **An evaluator over a mock origin** — what this is. Self-contained and
   provable here.
2. **C emitted against the VRT ABI**, for real `varnishd` to `dlopen` —
   what Varnish's own VCC does. Rejected because nothing here can compile
   or link it: it would be untested C against an ABI this repo cannot see.
3. **Emitting Fern source** — needs the same runtime as (1) *plus* a
   codegen layer, so strictly more work for less evidence.

What it implements:

- **The state machine.** `vcl_recv` → `hash`/`pass`/`pipe`/`purge`/`synth`,
  lookup into `vcl_hit` or `vcl_miss`, the backend side through
  `vcl_backend_fetch` and `vcl_backend_response`, and `vcl_deliver`. The
  ordered list of subroutines that ran is reported, because the path *is*
  the explanation for what a policy did.
- **The built-in subroutines.** A `vcl_recv` that falls through without
  returning continues into the built-in one, which passes any request
  carrying a `Cookie` or `Authorization` header — the rule VCL authors are
  caught by most. A response with no positive TTL is not stored.
- **Variable scoping.** Every read and write is checked against a table
  before it touches the transaction: `req` is not readable in a backend
  subroutine, `beresp` not outside the backend-response ones, `resp` only
  in deliver and synth, `obj` only on a hit, `obj.hits` read-only
  everywhere. A `call`ed subroutine is checked in its *caller's* context.
  An unknown name is rejected rather than read as empty.
- **Coercions.** `+` concatenates when either side is a string and adds
  otherwise; INT arithmetic stays integral (`beresp.status + 1` is `201`,
  not `201.000`); durations and byte sizes keep their families through
  arithmetic; byte units are 1024-based.
- **ACLs**, with **longest-prefix** matching rather than first-match —
  Varnish's rule, and the only one under which an exclusion works at all.
  In `acl a { "192.0.2.0"/24; ! "192.0.2.23"; }` the excluded host is also
  inside the `/24`, so first-match-wins would admit it and the `!` line
  would be dead.
- **A small builtin set**: `regsub` / `regsuball` (with VCL's `\1`
  backreferences), `hash_data`, `std.log`, `std.tolower`, `std.toupper`,
  `std.integer`, `synthetic`.
- **Bounded recursion**: mutually recursive `call`s and `return (restart)`
  loops both stop with an error instead of exhausting the stack.

## What it does not do

- **No network and no real origin.** `fetch` fabricates a response: 200
  unless the URL is `/status/NNN`, with a default 120s TTL standing in for
  a real `Cache-Control`. The tests drive error paths through that hook.
- **No VMOD objects.** `new rr = directors.round_robin();` parses and is
  then rejected at evaluation: real VMODs are `dlopen`'d shared objects and
  a Fern binary is static. The `std.*` functions above are shims.
- **`std/regex` is not PCRE2.** It is a Thompson NFA — no backreferences,
  no lookaround, `(?i)` only as a leading flag. Varnish's `~` is PCRE2 and
  real VCL uses the difference. Patterns outside the subset silently fail
  to match rather than erroring, which is the sharpest edge here.
- **No body handling**, so `synthetic()` logs rather than setting one.
- **The scoping table is not Varnish's complete matrix.** It encodes the
  well-known distinctions; what is in it is enforced, and an unknown name
  is an error, but a variable Varnish scopes more tightly may be accepted.
- **No inline C**, which is rejected at evaluation.

## Why it lives in this repo

A parser and a tree-walking evaluator are the densest ordinary users of
what a self-hosting language has to get right: recursive unions with struct
payloads, exhaustive `match`, arrays grown by `append`, and — since Fern
struct fields are immutable after construction — state threaded as a return
value rather than mutated. The evaluator is a pure function of (program,
transaction, subroutine), which is what lets the state machine hand a
transaction between subroutines and keep every intermediate state for the
log.

`internal/e2e/vcl_example_test.go` runs both TAP suites, the formatter
fixed-point check, the cache miss/hit path, the ACL matrix, and the scoping
rejection on every CI run.
