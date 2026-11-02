# Release verification checklist

Status: [reference] the gates a release passes, for [SEC-050](https://github.com/JakeChampion/lang/issues/13552).
Parent: [security epic](https://github.com/JakeChampion/lang/issues/13502).
Gate: `TestSecurityClaimsNameLiveGates` fails when a test or a lane this file names does not exist.

`release.yml` builds `fern` and the coreutils binary for linux-x86_64, linux-arm64 and darwin-arm64 every night and on dispatch. It signs and notarizes the macOS binaries, writes a SHA-256 sidecar per archive, and publishes the archives.
A release is the commit on `main` that the workflow ran on.
Before a release is announced, each item below has evidence. The evidence is a CI run URL or a command output recorded in the release notes.

## Gates

| Item | Evidence | Where |
|---|---|---|
| The full suite is green on the release commit | the `ci-main.yml` run for the commit, with no red lane and no open `main-red.yml` issue | `.github/workflows/ci-main.yml`, `.github/workflows/main-red.yml` |
| The self-built compiler reproduces itself on all three hosts | the `bootstrap.yml` run for the commit | `.github/workflows/bootstrap.yml` |
| The hardening tests pass on both native targets | `TestSelfHostStackCanary`, `TestSelfHostForwardEdgeCFI`, `TestSelfHostImagePermissions`, `TestSelfHostPIE`, `TestSelfHostShadowStack`, `TestSelfHostHardenArm64` in the self-host shards | `.github/workflows/test-e2e-selfhost.yml` |
| The adversarial index and the guarantees name live tests | `TestAdversarialSuiteIndexNamesLiveTests`, `TestSecurityClaimsNameLiveGates` | `.github/workflows/test-e2e-other.yml` |
| The fuzz smoke targets pass from their seeds | `FuzzSelfHostManifest`, `FuzzSelfHostIRVerify`, `FuzzSelfHostX86Assembler`, `FuzzSelfHostArm64Assembler` | `.github/workflows/fuzz-diff.yml` |
| The profile costs are within their baselines | the perf lane's profile-costs comparison | `.github/workflows/perf.yml`, `.github/profile-costs-baseline.txt` |
| Both shipped binaries were built for the matrix target with the safe profile | `fern -manifest` on `fern` and on `fern-coreutils` names the target and reads `harden: no`, `pie: no`, `sanitize: no`, `size: no` and `shared: no` | the manifest step of the build job in `.github/workflows/release.yml` |
| The security smoke matrix passes on the shipped linux-x86_64 compiler | `scripts/security-smoke` compiles the bounds and compile-fail cases on both native Linux targets, inspects each image, and writes the `security-smoke-report` artifact | the build job in `.github/workflows/release.yml` |
| The archive hashes match their sidecars | `shasum -a 256 -c` over the downloaded archives | the release page |
| The macOS binaries are notarized | the `macos-sign` action's notarization step | `.github/workflows/release.yml` |

## Refusals

A release is not announced when any gate above is red. It is not announced when a `main-red.yml` issue is open for the commit, or when a security report against the commit is unresolved.
A gate that was skipped is red, not green: a `SKIP` in a suite is a missing dependency to fix.

## Not yet in place

- The signed packslip lists the `fern` archives alone. The coreutils archives have the SHA-256 sidecar and no signature.
- The security smoke matrix runs on the linux-x86_64 build alone. The arm64 rows of that run are emulated.
- The release is the profile the Makefile builds. No gate reads a profile from the release notes or the tag.
