# Manifest package visibility and enum access

Issues: #12721 and #13077.

The loaders now record package identity from the nearest `fern.toml`.
Without a manifest, they use the module directory.
All standard library modules share one identity.
Reference checks compare cached identities.

The Go loader records package-scoped variants beside public variants.
The checker also filters bare constructors and patterns through their enum's permitted modules.
The self-host retains enum visibility in the combined module.
It restricts enum viewer lists through the same package identities.

Typed constructor checks choose a visible enum when a private enum shares its variant name.
Built-in variants remain available inside library modules when an application declares the same name.
The regression suites cover both cases.

The standard library uses `pub(package)` for its implementation helpers and `pvec.VNode`.
Generated JSON decoders retain access through their parser origin and trusted library owner.
Manual implementations follow the source visibility rule.

The three-stage bootstrap converges.
All 11 driver sizes pass the unchanged strict baseline.
The fixture matrices pass on x86-64, ARM64, and WebAssembly with balanced allocations.
The new fixture's allocation trace has zero unmatched allocations.

The [validation record](../benchmarks/manifest-package-2026-10-23/README.md) contains the source and binary identities.
