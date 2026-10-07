//go:build js || plan9

package interp

// disableCoreDumps on a platform with no resource limits: platforms refuses
// `disable_core_dumps` on the wasm worlds, so nothing that type-checks for
// this target reaches it.
func disableCoreDumps() bool { return false }
