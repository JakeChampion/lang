# Shared audio CI prerequisites

The codec branches need the same compiler and Android repairs.
Separate CI runs inherit the other missing repairs from main.
This branch combines the completed repairs into one reviewable tree.
The codec features and benchmark changes retain their own pull requests.

The compiler repairs correct receiver roots, local ownership scopes, builtin enum qualifiers, and WAT shuffle indices.
Their focused native, WASI, Android compilation, and checker controls pass.
The supported build of the combined compiler sources passes.
Its core SHA-256 is `b2f27f1a946c42477cf6e7552c5a7161b4b37f5c8467e2bd3294b4e0d6588bfd`.
The source notes for each repair retain the controls and limitations.

The Android runner now requests the platform video read permission for its MediaStore fixtures.
CI logs identify the prior missing permission and the invalid shell URI grant.
Eight player controls and three video controls pass.
The device lane must confirm complete content playback.

The branch also includes the existing linear-audio lint repair from `7932d971a47bb0513dae0c3639c5777133ea5472` in #14757.
The complete checker, module-loader, and source-lint packages pass.
The modern-form ratchet passes without a ceiling change.
The performance changes include the measured reductions from #14794 and raise no limit.

Fresh controls identified stale compiler expectations, missing global constant source sites, and missing socket-option filter values.
The follow-up repairs for #14790, #14791, and #14792 retain exact runtime and source-site controls.
Their source note describes the changes and the checks.
The branch must pass those controls and full CI before merge.
No merge or complete CI success follows from the focused results.

The later driver extraction for #14800 restores the complexity gate without a ceiling change.
The supported build passes with core SHA-256 `65ead4d702d1ee8a3f96985c7cb8277847a7d50d4db37b32eff2ec4d51f83ba8`.
The shared ownership fixture now has 19 controls, including loop jumps and initializer moves.

Parent: #14509. Main gate: #14566. Roadmap: #14508.
