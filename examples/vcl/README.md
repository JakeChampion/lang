# A VCL front end and evaluator, in Fern

A lexer, parser, syntax tree, printer, **and evaluator** for the **Varnish
Configuration Language** (VCL 4.0 / 4.1) — the language Varnish Cache users
write to express caching policy. Written in Fern, depending on nothing
outside `std`.

```
$ fern -interp vclfmt.fern   -- testdata/sample.vcl        # parse + reformat
$ fern -interp vclcheck.fern -- testdata/policy.vcl        # validate, don't run
$ fern -interp vclrun.fern   -- testdata/policy.vcl GET /static/app.css -n 2
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

Three drivers: `vclfmt` parses and reprints, `vclcheck` validates without
running, and `vclrun` executes a request against a policy — checking it
first, as `varnishd` does at load time. All exit 0 on success, 1 on a VCL
error, 2 when the file cannot be read or parsed.

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
| `check.fern` | Static validation: scoping, return actions, names, reachability. |
| `eval.fern` | Expression evaluation, statement execution, ACL and regex matching. |
| `machine.fern` | The request state machine and the built-in subroutines. |
| `vclfmt.fern` / `vclcheck.fern` / `vclrun.fern` | The drivers. |
| `vcl_test.fern` / `vclbackend_test.fern` / `vclcheck_test.fern` | 41 + 38 + 32 TAP cases. |

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

**Fern does not emit C, and will not.** That rules out the route Varnish's
own VCC takes — emitting C against the VRT ABI for `varnishd` to compile
and `dlopen` — as a matter of project direction, not convenience. It is
also the route this repo could never test: there is no `varnishd` and no
`libvarnishapi` here, so it would be untested C against an ABI nothing in
the tree can see. Both reasons point the same way; the first is the one
that settles it.

That leaves two, and this is the first:

1. **An evaluator over a mock origin** — what this is. Self-contained, and
   every rule it implements is provable in-repo.
2. **Compiling VCL to Fern source**, handed to `fern` for a native binary.
   The natural next step: it reuses this runtime wholesale and swaps the
   tree-walk for generated code, with no new backend and no C anywhere.

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

## The checker

The evaluator enforces scoping as it runs, so a mistake surfaces only on
the request that reaches it — a `req.url` read in `vcl_backend_response`
looks fine until something misses the cache. `check.fern` moves those
checks to load time and reports **every** problem in one pass:

```
$ fern -interp vclcheck.fern -- broken.vcl
broken.vcl:8:1:   [] 'vcl_recieve' is not a Varnish subroutine, so it would never run
broken.vcl:13:5:  [vcl_recv] 'resp.http.X' is not writable in vcl_recv
broken.vcl:14:21: [vcl_recv] unknown name 'nosuchacl'
broken.vcl:14:34: [vcl_recv] 'deliver' is not a valid return from vcl_recv (expected one of: hash, pass, pipe, purge, synth, restart, fail)
broken.vcl:15:5:  [vcl_recv] call to undefined subroutine 'missing_helper'
broken.vcl:5:29:  [vcl_backend_response] 'req.url' is not readable in vcl_backend_response
```

The walk is **per entry point, not per subroutine**, because VCL's scoping
is contextual: a `call`ed subroutine runs in its caller's context. A helper
reachable from both `vcl_recv` and `vcl_backend_response` is checked twice,
once in each — which is how the last line above finds a `req.url` read
written inside a helper and illegal only because of who calls it. No
per-subroutine walk can see that.

It also checks return actions against each subroutine's legal set (naming
the alternatives), rejects `vcl_`-prefixed subroutines Varnish would never
call, catches duplicate and undefined subroutines, requires `hash_data()`
to sit in `vcl_hash`, and lists subroutines nothing can reach.

Each entry in the variable table carries the subroutines that may read and
write it, and both the checker and the evaluator consult that one table —
so they cannot disagree about what is legal.

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

`internal/e2e/vcl_example_test.go` runs all three TAP suites, the formatter
fixed-point check, the cache miss/hit path, the ACL matrix, and the
load-time rejection of a bad policy on every CI run.
