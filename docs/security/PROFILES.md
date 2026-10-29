# Security profiles and the mitigation status manifest

Status: implementation for [SEC-046](https://github.com/JakeChampion/lang/issues/13548).
Parent: [security epic](https://github.com/JakeChampion/lang/issues/13502).
The flags a profile sets are described in `docs/HARDENING.md` and `docs/SANITIZER.md`, and the process report in [MITIGATION-REPORT.md](MITIGATION-REPORT.md).

## Profiles

`-profile NAME` sets a flag set before the explicit flags apply, so an explicit flag adds to the profile.

| Profile | Flags | Use |
|---|---|---|
| `safe` | the defaults: bounds checks, arena checks, write-xor-execute, a non-executable stack, the backtrace walk | every build |
| `hardened` | `-harden -pie` | a program that reads untrusted input on a native Linux target |
| `diagnostic` | `-sanitize -backtrace -g` | finding a memory misuse |
| `minimal` | `-size` | the smallest image, which drops the backtrace walk unless `-backtrace` is given |

A profile changes no language semantics.
A bounds check, an arena check or an integer rule holds the same way under every profile, and a profile only adds detection or hardening.

A profile fails closed.
`-profile hardened` on a target without `-harden`, such as wasm or Darwin, is the same refusal as `-harden` itself, with exit 2.
An unknown profile name exits 2 and names the four profiles.

## The build manifest

Every native executable carries one line in its read-only data: `FERNBUILD1 target=... profile=... harden=... pie=... sanitize=... backtrace=... size=... shared=... heap_limit=... quarantine=...`.
`fern -manifest IMAGE` prints it one field per line, and answers exit 1 with a cause for a file without one.
The line is deterministic, so a reproducible build stays reproducible.

## Status at run time

`mitigations.manifest()` returns one row per feature with five cells, which the same program prints as `name compiled=... supported=... requested=... enabled=... verified=...`.

| Cell | Source |
|---|---|
| `compiled` | the build manifest in `/proc/self/exe` |
| `supported` | the `cpu` and `kernel` cells of `mitigations.report()`, or `yes` for a software feature |
| `requested` | what the compiled image asks the kernel for at startup |
| `enabled` | the `process` cell of the report for a hardware feature, and the `compiled` cell for a software one |
| `verified` | the test that pins the feature and whether it ran natively, under emulation, or not yet |

The rows are `nx-stack`, `canary`, `cfi`, `pie` and `sanitizer` on both targets, `shadow-stack` and `ibt` on x86-64, and `pac`, `bti` and `mte` on arm64.
A program without a manifest, which an interpreted or a wasm program is, reads `unknown` in every `compiled` cell.
A cell never claims more than its source: `ibt` is `requested=no` and `verified=unavailable` on every Linux host, and `shadow-stack` stays `verified=unverified` until a host with the feature runs the fault test.

## Verification

`TestSelfHostProfiles` builds each profile on both targets, reads the manifest back with `fern -manifest`, runs the image, and prints the run-time status of a hardened build.
The `std mitigations` suite covers `build_line` and `parse_build` with synthetic images and a manifest with unknown fields.
The refusals of a bad name and of a profile the target cannot carry exit 2.

## Remaining limits

- A shared object carries no manifest, and a wasm or Darwin image carries none either.
- The `verified` cell is a fixed text per feature, not a measurement on the running host.
- The manifest is a line in the image, not an ELF note, so a generic tool reads it with `strings` rather than `readelf -n`.
