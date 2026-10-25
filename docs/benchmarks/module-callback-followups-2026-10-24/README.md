# Struct namespaces and pattern callback closures

This record covers #13146 and #13149.
The measured source is `54d091064a377c379610bcf3d9968daca11e025f`.
The dependency source is `191c0c1a05e15db00e465bf14cfd7eb09b05c29b`.

The Go loader qualifies an imported module's own struct in a static method call.
This permits calls such as `Input.from_json(text)` within that module.
Local values and type parameters retain the Go loader's scope guards.
The regression checks associated functions, a derived JSON reader, and both scope guards.

The native checker carries each match arm's payload types into its nested closure return checks.
This also covers `if let`, which becomes a match.
A callback from `Option[(i32) => void]` therefore keeps its function type inside a closure.
The tests cover Option, Result, two nested closures, and callbacks that return a value.
A Cell counter verifies that all three expected callbacks run.
A void closure that calls a non-void callback still receives E002 with the actual i32 result type.

All 2,279 Go cases pass across three packages without skips.
All 224 selected native cases pass without skips.
The native count combines the focused cases and the two broader suites, with no duplicate parent tests.
The published stage0 pin produces identical stages 2 and 3 at 14,461,808 bytes.
Their SHA-256 is `efd9f2ba6ed9eea4b28213afed61e87f182752ccc3c52ac60ab7e91e1bbcca13`.
All 11 strict size gates pass without baseline changes.

The final matrix passes 53 commands with 12 balanced allocation counts and zero live bytes.
It checks both interpreters, three execution targets, four Darwin builds, and seven invalid callback refusals.
Darwin images are not executed locally.

One generic scope control runs only in the Go frontend.
The native type parameter suite retains its stated module-type precedence for value qualifiers.
This policy accounts for the earlier exploratory matrix refusal.
The source did not change after that refusal.

Source format, Go format, Go vet, the feature census, and the complexity limit pass.
The capsule retains the baseline failures, exact source patch, inputs, command output, and hashes.
