# Manifest package visibility

This historical record covers #12721 and #13077.
The [current integration](current-main-integration/README.md) records the complete helper migration and public re-export checks.
The base commit is `671c1109abd94f82e590ddbcd5e07f726c127006`.
The feature commit is `0c800ea765e6db37b7fbd3c49f10e862649aea3e`.
The compiler source commit is `ccd6b58eb64a25dffa6f2d35541a1040dcdfcd0f`.

## Behavior

Both loaders use the nearest manifest path as the package identity.
Without a manifest, the directory defines the package.
The standard library is one package across `std` and `core`.
Nested manifests define separate packages even when their package names match.
Each loader caches the identity before it changes symbol names.

Enum constructors and patterns follow their enum's visibility.
Hidden variants cannot replace a public variant with the same name.
An application's enum cannot hide a built-in variant inside an imported module.

The library migration covers 76 declarations:

- 57 Map runtime helpers.
- Three integer helpers.
- Two string helpers.
- 13 JSON derive helpers.
- The `pvec.VNode` enum.

Parser-generated `FromJson` methods retain access to the JSON helpers.
Source-written methods cannot call those helpers from another package.
The self-host check verifies the generated method's origin and the helper's library owner.

At the feature commit named above, documented utilities with external callers remained public.
These include `f64.pow10_*`, decimal buffer writers, `digit_pairs`, and `io_buffered.BufBlock`.
The current library already removed or hid the other node types listed in #12721.

## Validation

| Gate | Result |
| --- | --- |
| Three-stage bootstrap | Stages 2 and 3 are byte-identical |
| Driver sizes | All 11 rows pass the unchanged strict baseline |
| Go parser, checker, and loader suites | Pass |
| Self-host checker and visibility suites | Pass, including the complexity limit |
| Public library regressions and standalone imports | Pass |
| Official fixture runners | Five cases on each of six runners, 30 selected executions |
| Public API matrix | 90 commands pass |
| Manifest project matrix | 19 commands pass |
| Allocation trace for the new fixture | 434 events, zero unmatched allocations |
| Embedded release CLI | Public operations pass and private helper access fails |
| Source format check | All 1,179 standard source files pass |
| Source lint and Go vet | Pass |

The native, ARM64, and WebAssembly matrix runs use `FERN_STRICT_IR`, `FERN_SANITIZE`, and `FERN_LEAKCHECK`.
Each run requires balanced allocations and zero live bytes.
The project matrix also verifies that refused builds produce no output artifact.
The final test records contain no skipped tests.

The Go frontend suites used commit `161b8bcf0d06dc89d48af26bfca0fa0fd1df45bf`.
The Go frontend and library sources did not change after that commit.
The final oracle includes the feature commit's VCS identity.

## Artifacts

`manifest.json` records source identities, binary hashes, test counts, and artifact hashes.
The source hash covers tracked compiler and library paths and their contents.
Each path and each file content ends with a NUL byte in that hash input.

The stage 2 and stage 3 SHA-256 is:

```
7eeb5d226d79bfbd2f91e752ee5aac051b8c1bd35283019c1a8855eb2e939599
```

The record includes the baseline refusals and enum visibility disagreements.
The compressed source patch restores the feature from its base commit.
The matrix scripts and their command records preserve the executable checks.
