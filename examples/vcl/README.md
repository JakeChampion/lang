# A VCL front end, evaluator, compiler, and proxy — in Fern

A lexer, parser, syntax tree, printer, **and evaluator** for the **Varnish
Configuration Language** (VCL 4.0 / 4.1) — the language Varnish Cache users
write to express caching policy. Written in Fern, depending on nothing
outside `std`.

```
$ fern -interp vclfmt.fern   -- testdata/sample.vcl        # parse + reformat
$ fern -interp vclcheck.fern -- testdata/policy.vcl        # validate, don't run
$ fern -interp vclrun.fern   -- testdata/policy.vcl GET /static/app.css -n 2
$ fern -interp vclc.fern     -- testdata/policy.vcl > policy.fern   # compile
$ fern -target x86-64-linux -o policy policy.fern && ./policy GET /
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

Five drivers: `vclfmt` parses and reprints, `vclcheck` validates without
running, `vclrun` executes a request against a policy — checking it first,
as `varnishd` does at load time — `vclc` compiles a policy to Fern source
for `fern` to turn into a native binary, and **`vclproxy` is a real HTTP
caching reverse proxy** you can put in front of an origin and `curl`. The
first four exit 0 on success, 1 on a VCL error, 2 when the file cannot be
read or parsed.

```
$ fern -target x86-64-linux -o vclproxy vclproxy.fern
$ fern -target x86-64-linux -o origin   origin.fern
$ ./origin 9000 &
$ ./vclproxy testdata/proxy.vcl 8080 &
$ curl -s http://127.0.0.1:8080/page ; curl -s http://127.0.0.1:8080/page
origin-hit=1 path=/page
origin-hit=1 path=/page          # unchanged: the origin was never asked again
```

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
| `compile.fern` | VCL to Fern source: field accesses, folded constants, ANF. |
| `wire.fern` | HTTP/1.1 on the wire: build a backend request, parse a reply. |
| `store.fern` | The cache's between-request encoding. |
| `report.fern` | The request command line and transaction report, shared by both drivers. |
| `eval.fern` | Expression evaluation, statement execution, ACL and regex matching. |
| `machine.fern` | The request state machine and the built-in subroutines. |
| `vclfmt.fern` / `vclcheck.fern` / `vclrun.fern` / `vclc.fern` | The offline drivers. |
| `vclproxy.fern` / `origin.fern` | The proxy, and a counting origin to put behind it. |
| `vcl_test` / `vclbackend_test` / `vclcheck_test` / `vclcompile_test` / `vclwire_test` | 41 + 40 + 32 + 26 + 23 TAP cases. |

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

## The compiler

`vclc` turns a checked policy into a Fern module, which `fern` turns into a
native binary. That is the second of the two routes left once C is out, and
it needed no new backend:

```
$ fern -interp vclc.fern -- testdata/policy.vcl > policy.fern
$ fern -target x86-64-linux -o policy policy.fern
$ ./policy GET /static/app.css -n 2      # a 285 KB standalone binary
```

What compiling buys over the tree-walk:

- **A variable becomes a struct field read.** `req.url` compiles to
  `t.req.url` — no table lookup, no scope test. The check already happened,
  so none of it survives to run time. `vclcompile_test` asserts the
  generated source contains no `vars.read` at all.
- **An ACL becomes a function with its masks folded.** `"192.0.2.0"/24`
  emits `(ip & 4294967040) == 3221225984`, and longest-prefix-wins becomes
  generated control flow.
- **Constants fold.** `1h` is `VDuration(3600.0)`; `256KB` is
  `VBytes(262144)`.
- **Control flow becomes Fern control flow.** An `if` is an `if`.

Expressions compile to **A-normal form** — each subexpression into its own
local, with an explicit check after anything that can fail. That is more
verbose than nesting calls, and it is what keeps compiled and interpreted
behaviour identical: `&&` short-circuits through a real branch, so a
`std.log` on the right does not run when the left already decided, and an
arithmetic type error reports the message the interpreter reports.

The **state machine is not generated**. `machine.fern` takes a `Driver` —
a function that runs one subroutine, plus the declared backends — so the
interpreter supplies a closure over the parsed tree while a compiled policy
supplies its own dispatcher. The graph is identical for every policy, so
there is one copy of it.

The gate that matters is differential, not textual:
`TestVCLCompiledMatchesInterpreted` compiles the sample policy, runs it over
eleven requests chosen to take different routes through the state machine,
and diffs every byte against `vclrun`. A string assertion can only say the
compiler emitted what was intended; the differential says the emitted code
*means* the same thing — and it is what catches a codegen table drifting
from the runtime table it mirrors.

## The proxy

`vclproxy` is the whole stack doing its job at once: a real listening
socket, an HTTP/1.1 request parsed off the wire, the policy's subroutines
deciding what happens, a real TCP fetch from the declared `backend` on a
miss, a cache that survives between requests, and a real response written
back. The policy is parsed and **checked once at startup**, exactly as
`varnishd` loads VCL — a policy with a scoping error never reaches the
accept loop.

`origin.fern` is what makes the caching *provable*. Every response carries
the number of requests that process has actually served, so a second
request answered with the **same** counter means the origin was never
asked. Reading the proxy's own headers could never establish that.

Measured through a real client:

| request | body | meaning |
|---|---|---|
| `/a` ×3 | `origin-hit=1` each time | cached — origin contacted **once** |
| `/b` | `origin-hit=2` | different hash key → separate object |
| `/nocache` ×3 | `origin-hit=3,4,5` | `return (pass)` never caches |
| `/a` again | `origin-hit=1` | still cached |

The backend's `.host` and `.port` are read from the VCL. The origin's
`Cache-Control: max-age` sets the object's TTL, which is how a real origin
controls a cache. `vcl_deliver` can set `resp.http.X-Cache-Hits` from
`obj.hits` and watch it climb across requests.

Three things about the runtime are worth knowing, because they shaped the
design rather than being incidental:

- **`Cell` — the only thing that outlives a request — holds a scalar or a
  string, nothing composite** (E057: a composite could form a cycle). So
  the cache persists as an encoded *string*, decoded at the top of each
  request and re-encoded at the end. `store.fern` uses a length-prefixed
  encoding rather than a delimited one, because a cached body is arbitrary
  bytes and will eventually contain whatever separator you picked. The cost
  is real and stated: encode/decode is O(total cached bytes) per request,
  so this is a demonstration store, not a storage engine.
- **`tcp_serve` does not surface the peer address**, so `client.ip` comes
  from `X-Forwarded-For` when present and is `127.0.0.1` otherwise. An ACL
  on `client.ip` is therefore only as trustworthy as whatever sets that
  header.
- **The accept loop is single-threaded**, which is why an unsynchronised
  `Cell` is sound here and would not be under concurrency.

The sockets are native builtins, absent from the interpreter: `vclproxy`
and `origin` compile and run, they do not work under `fern -interp`.

## What it does not do

- **The offline drivers still use a mock origin.** `machine.mock_fetch`
  fabricates a response — 200 unless the URL is `/status/NNN` — so
  `vclrun`, `vclc` and the test suites need no network. The proxy swaps in
  a real fetcher through the same `Driver` seam; nothing above it changes.
- **No chunked transfer-encoding.** The proxy sends `Connection: close` and
  advertises no `TE`, so a conforming origin will not chunk; a body that
  arrived chunked would be passed through with its framing still in it.
  This is the one wire-level shortcut, and it is why the proxy is a
  demonstration rather than something to put in front of traffic.
- **No TLS, no keep-alive, no HTTP/2.**
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

`internal/e2e/vcl_example_test.go` runs all five TAP suites, the formatter
fixed-point check, the ACL matrix, the load-time rejection of a bad policy,
the compiled-vs-interpreted differential, and a native build of a compiled
policy. `internal/e2e/vcl_proxy_test.go` builds the proxy and the origin,
puts one in front of the other, and drives them with a real HTTP client —
on every CI run.
