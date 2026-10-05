package e2eselfhost

import (
	"strings"
	"testing"
)

// Fusion already proves each callback's identity. Losing that identity at
// physical lowering leaves a closure-slot lookup and indirect call per element.
func TestSelfHostArrayFusionUsesKnownCallbackBodies(t *testing.T) {
	gcc, runner, driver := buildModloadDriverX86(t)
	asm, dir := compileSourceModload(t, runner, driver, selfHostArrayFusionSrc)
	for _, name := range []string{
		"via_map_fold", "via_filter_map_fold", "via_map_map_reduce",
		"via_order_sensitive", "via_filter_reduce", "via_capture",
	} {
		t.Run(name, func(t *testing.T) {
			body := emittedBody(t, string(asm), "__fn_"+name)
			if strings.Contains(body, "call *") || strings.Contains(body, "callq *") {
				t.Fatalf("resolved fused callback still dispatches indirectly:\n%s", body)
			}
		})
	}
	bin := buildBin(t, gcc, dir, "fusion-dispatch", asm)
	if out, err := runX86_64Bin(runner, bin).CombinedOutput(); err != nil {
		t.Fatalf("fused direct callbacks: %v\n%s", err, out)
	}
}
