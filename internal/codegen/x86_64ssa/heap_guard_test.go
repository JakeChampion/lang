package x86_64ssa

import (
	"fmt"
	"slices"
	"sort"
	"strings"
	"testing"
)

// A helper whose body names the arena, the guard, the alloc-preserve
// trampoline or the allocator itself needs a heapUsingHelpers entry, or `heap`
// stays false for a module whose only call is to it and _start emits neither
// the reservation nor the guard. The helper then references symbols the
// module never defines, and the link gate refuses it — while the same program
// builds under the flat emitter. arm64ssa's twin of this test exists because
// that list drifted there; __fern_scale_f64 was the first to drift here.
//
// A direct `call __alloc` has a second requirement: runtimeHelperDeps must
// carry the edge, since the allocator is otherwise emitted only when the text
// reaches the trampoline, which a direct call never does.
func TestEveryAllocatingHelperIsRegistered(t *testing.T) {
	names := make([]string, 0, len(runtimeHelperEmitters))
	for name := range runtimeHelperEmitters {
		names = append(names, name)
	}
	sort.Strings(names)
	allocCall := "call " + fnLabel("__alloc") + "\n"
	for _, name := range names {
		var b strings.Builder
		w := func(format string, args ...any) {
			fmt.Fprintf(&b, format, args...)
			b.WriteByte('\n')
		}
		runtimeHelperEmitters[name](w)
		body := b.String()
		callsAlloc := strings.Contains(body, allocCall)
		for _, sym := range []string{heapPtrSym, heapGuardSym, allocPresSym} {
			if strings.Contains(body, sym) {
				callsAlloc = true
				break
			}
		}
		if callsAlloc && !heapUsingHelpers[name] {
			t.Errorf("%s reaches the arena but is not in heapUsingHelpers, so a module whose only call is to it emits no heap", name)
		}
		if strings.Contains(body, allocCall) && !slices.Contains(runtimeHelperDeps[name], "__alloc") {
			t.Errorf("%s calls __alloc directly but runtimeHelperDeps carries no __alloc edge, so a module whose only allocation is its would not link", name)
		}
	}
}
