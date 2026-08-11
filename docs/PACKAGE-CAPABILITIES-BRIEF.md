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
- **Native runtime attenuation now exists too** (#6071): `FERN_SANDBOX=1`
  installs a seccomp-bpf filter at `_start` permitting exactly the
  syscalls the emitted binary can issue. It does **not** derive from
  this brief's capability grants, and deliberately so — `caps.Analyze`
  models user-callable builtins, not the runtime's own mmap /
  exit_group / write / clock_gettime, so a grant-derived filter would
  need a hand-maintained floor that rots silently. It derives from the
  backend's recorded syscall set instead. The relationship is
  complementary in kind: this brief's checker rule proves what code
  *can call*; the filter constrains what the process can do once
  control flow has been *hijacked*. Neither subsumes the other.
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
  load time from the manifests alone (see phase 3 for why load, not
  `-resolve`).
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

   **Status: shipped (#5361).** Dependency entries accept
   `capabilities = ["net", …]` on every form (path / url / workspace /
   version); an unknown name is a manifest error naming the vocabulary
   (`internal/manifest.parseCapabilities`). modload records each
   governed dependency's grant on `ast.Program.CapGrants` (keyed by
   the dep's resolved directory — `declaredDepDir` is shared with
   import resolution so the two can't disagree), and cmd/fern's
   `enforceCapabilities` runs after every successful type-check
   (`-check`, `-interp`, and compile alike): `caps.Enforce` filters
   the SAME `caps.Analyze` rows the report prints — report = all
   usage, enforcement = usage minus grants. A governed package
   reaching outside its grant is **E070** with the chain; a package
   with no `capabilities` key is **warn-and-allow live today**
   (stderr, once per package+capability, with an example chain) —
   the default-deny flip is the pending follow-up; the root package
   is never enforced or warned.
3. **Attenuation.** Transitive subset rule at `-resolve` time.

   **Status: shipped (#5361).** Checked at LOAD time (modload's
   `capGrants`) rather than `-resolve`, because `-resolve` only walks
   the versioned (MVS) slice of the graph while the load reads every
   dependency form's manifest — path / url / workspace / versioned /
   vendored — so one check covers them all. Each granting edge is
   held to ITS grantor's holdings independently: a manifest whose own
   package is governed (some parent's dependency entry grants it a
   `capabilities` key; holdings = the union of those grants) granting
   a dependency a capability outside that union is a load error in
   the manifest-error family, naming the granting `fern.toml` —
   `dependency "b" of "a" is granted 'net' but "a" itself holds only
   [fs] (attenuation: a dependency may grant at most what it holds)`
   — with all violations reported at once in deterministic order
   (manifest dir, dep name, capability). An ungoverned grantor
   imposes no ceiling (that's the warn-and-allow era; the root, which
   nothing declares, falls out as ungoverned and stays unrestricted).
   Diamonds keep phase 2's union: a package granted by several
   parents holds the union of the granting edges, but each edge must
   pass its own grantor's ceiling — a sibling's legitimate grant of
   the same capability never excuses an amplifying edge.
4. **Runtime alignment.** When the WASI component path matures
   (#4315–#4320 lane), derive the component's requested WASI imports
   from the same table, so the compile-time story and the runtime
   sandbox agree.

## The self-host port

The self-hosted compiler carries its own copy of phases 1 and 2 in
`examples/self_host/caps.fern` (#6634) — vocabulary, builtin
classification, the reachability walk, the report, and E070 — because
it cannot import Go. `fern.fern` runs the report behind
`-capabilities` and enforcement on the compile / `-check` / `-interp`
paths, exactly where cmd/fern does.

Three things about that copy are worth knowing before touching either
side:

- **The classification is pinned, not copied by hand.**
  `internal/caps/selfhost_parity_test.go` reads the Fern tables as data
  and compares them with `BuiltinCaps` / `Ungated` / `Capabilities`
  entry-for-entry, so a builtin tagged on one side and not the other
  fails a fast Go test. `frontend_ungated()` is the one list with no
  native counterpart: names the self-host front end registers as
  builtins that native's checker does not (`len`, `chr`, `print_int`,
  …), each held to being absent from native's registry.
- **Package identity comes from the ENTRY manifest.** Native resolves a
  module's package from the nearest governing `fern.toml`; the
  self-host driver's loader resolves every import from the entry
  directory, so a dependency's own dependencies are not reachable and
  there is no deeper package to attribute. Deps are resolved through
  the manifest (path / workspace / lock / vendor / store) by
  `modloader.resolve_module_src` — before #6634 the driver could not
  load a manifest dependency at all.
- **The example chain is spelled differently.** Both compilers mangle
  an imported function as `<module>__<name>`, but native's module is
  the dependency's lib FILE (`lib__save`) and the self-host's is the
  name the import used (`helper__save`). The severity, package,
  capability and builtin all match exactly — the differential in
  `internal/e2eselfhost/self_host_caps_test.go` compares those and
  normalises the chain.

Phases 3 and 4 have no self-host counterpart yet: attenuation is a
load-time check over a dependency graph the self-host loader does not
build.

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
