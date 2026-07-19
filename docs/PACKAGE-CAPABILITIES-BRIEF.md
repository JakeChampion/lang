# Per-package capability grants — design brief

From the 2026-07 PLT landscape survey (`PLT-LANDSCAPE-2026.md` §2.5).
Prior art: Deno's permission flags (per *process*), WASI's capability
handles (per *component*), the object-capability tradition (E,
Austral), and the npm/PyPI supply-chain incidents that made "what can
this dependency actually DO?" a mainstream question.

## The thesis

Fern can answer that question at **compile time, per package** —
finer than Deno's process grants and cheaper than WASI's runtime
handles — because three properties already hold:

1. **A closed import graph.** `modload` resolves every module of
   every package; there is no dynamic loading and no ambient FFI.
2. **A manifest boundary.** `fern.toml` names each dependency
   (`PACKAGES.md`); the loader knows exactly which package every
   function came from.
3. **A small, closed I/O surface.** Programs reach the outside world
   only through a bounded set of builtins (tcp_*, file ops, `env`,
   `subprocess`, clock/random, …). No builtin call, no I/O.

So "this package can use the network" is a static reachability fact
the checker can enforce, with a diagnostic, before anything runs.

Relationship to existing tracks — complementary, not competing:

- The WASI-level attenuation noted as out-of-scope in #4907 guards
  the *process/component* boundary at runtime; this guards the
  *intra-binary package* boundary at compile time. Both can exist.
- `PLATFORM-RESEARCH.md`'s `Platform` capability bag governs what the
  *host* offers the *application*; this governs what the
  *application* delegates to its *dependencies*. Its open question 3
  (checker error vs link-time symbol absence) is answered here:
  checker error, with a source position and a dependency chain.

## Surface

```toml
[dependencies]
json_fast = { url = "…", sha256 = "…", capabilities = [] }
kv_client = { path = "../kv", capabilities = ["net"] }
```

Capability vocabulary (v1, deliberately coarse): `net`, `fs`, `env`,
`subprocess`, `time`, `random`. Coarse buckets are auditable;
path-level filtering (à la Deno's `--allow-read=/tmp`) is explicitly
out of scope for v1.

Rules:

- **Root is unrestricted.** The root package (and the stdlib it calls
  directly) needs no grants — the program's author is trusting
  themselves. Grants restrict *dependencies*.
- **Default deny.** A dependency with no `capabilities` key gets
  `[]`. (Migration: a warning-only mode first — see phases.)
- **Attenuation, not amplification.** A dependency may grant its own
  deps at most the capabilities it holds itself. Checked at
  resolve time from the manifests alone.
- **Enforcement is reachability.** For each package, the checker
  walks its functions' call graphs; reaching a capability-tagged
  builtin (directly or through a transitive callee in the same or a
  deeper package) without the grant is an error carrying the chain:
  `json_fast → helper.fetch_cached → tcp_connect: package
  'json_fast' has no 'net' capability`.
- **Closures and `dyn` values** count at their *definition* package,
  not their call site — a callback the root hands to a dependency
  doesn't need the dependency to hold the callback's capabilities.
  (This is the object-capability reading: passing a closure IS
  passing a capability, deliberately.)

## Phases

1. **Inventory + report mode.** Tag the I/O builtins with their
   capability in one table; add `fern -capabilities`, which prints
   the per-package capability usage of the current program (computed,
   not declared). Zero enforcement, immediately useful for auditing,
   and it hardens the builtin→capability table before any error
   exists.

   **Status: shipped (#5361).** `internal/caps` holds the table
   (`BuiltinCaps` + the `Ungated` allowlist; completeness tests
   enumerate the checker and interp builtin registries and fail on an
   unclassified addition) and the reachability walk; `fern
   -capabilities FILE.fern` prints one sorted line per package —
   `app  fs,net  (example: main → lib__save → write_file)` — with
   stdlib usage folded into the calling package's row (the
   entry-point-altitude answer to the open question below). Phase 1
   reports *declared* reachability (every declared function is a
   root, uncalled or not), counts closures at their definition
   package, and follows calls into deeper packages, mirroring the
   enforcement rule above.
2. **Enforcement.** `capabilities` key honoured; violations are
   checker errors (new E-code) when the key is present; absent key =
   warn-and-allow for one release, then default-deny.
3. **Attenuation.** Transitive subset rule at `-resolve` time.
4. **Runtime alignment.** When the WASI component path matures
   (#4315–#4320 lane), derive the component's requested WASI imports
   from the same table, so the compile-time story and the runtime
   sandbox agree.

## Non-goals

- No runtime checks on native — this is a static property.
- No object-capability *values* in the language (no `Cap` types);
  closures already serve that role.
- No sandboxing of the root package.
- No path/host-level filtering in v1.

## Open questions

- Should `time`/`random` be capabilities at all in v1, or start with
  the clearly-exfiltration-relevant three (`net`/`fs`/`subprocess`) +
  `env`? Leaning: start with four, add `time`/`random` only if DST
  (`DST-PLATFORM-BRIEF.md`) wants "package is sim-pure" as a checkable
  property.
- Stdlib partitioning: `std/fetch` obviously implies `net` for its
  *caller's package* — the table must tag stdlib entry points, not
  just raw builtins, so grants read at the right altitude.
