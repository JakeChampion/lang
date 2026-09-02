package arm64ssa

import "testing"

// The elision is only sound while the body really is a bare `ret`, so the set
// is derived from the emitted text. These pin the derivation in both
// directions: a helper that does nothing is in it, and one that does something
// is not — which is what makes the freelist slice (docs/SSA-RC-RUNTIME.md,
// RC-4+) restore the call sites by itself when it gives these bodies work.
func TestNoOpDerivationFollowsTheBody(t *testing.T) {
	noop := noOpHelpers()
	for _, name := range []string{"__fern_box_free", "__free"} {
		if !noop[fnLabel(name)] {
			t.Errorf("%s is a bare ret today but is not recorded as a no-op", name)
		}
	}
	for _, name := range []string{"__fern_rc_inc", "__fern_rc_dec", "__fern_rc_is_unique", "__alloc"} {
		if noop[fnLabel(name)] {
			t.Errorf("%s has a real body but is recorded as a no-op — eliding its call would drop work", name)
		}
	}
}
