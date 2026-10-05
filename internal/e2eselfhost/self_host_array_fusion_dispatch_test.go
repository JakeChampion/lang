package e2eselfhost

import (
	"strings"
	"testing"
)

// Fusion already proves each callback's identity. Losing that identity at
// physical lowering leaves a closure-slot lookup and indirect call per element.
func TestSelfHostArrayFusionUsesKnownCallbackBodies(t *testing.T) {
	gcc, runner, driver := buildModloadDriverX86(t)
	// Callback preparation must expose a chain of ordinary scalar helpers.
	src := strings.Replace(selfHostArrayFusionSrc, "=> x * (3 as i64)", "=> fusion_scale(x)", 1)
	if src == selfHostArrayFusionSrc {
		t.Fatal("helper callback fixture was not inserted")
	}
	src += `
function fusion_three(): i64 { return 3 as i64; }
function fusion_scale(x: i64): i64 { return x * fusion_three(); }
`
	names := []string{
		"via_map_fold", "via_filter_map_fold", "via_map_map_reduce",
		"via_order_sensitive", "via_filter_reduce", "via_capture",
	}
	// Keep each assembly subject separate while allowing its callbacks to inline.
	for _, name := range names {
		src = strings.Replace(src, "function "+name+"(", "@noinline function "+name+"(", 1)
	}
	asm, dir := compileSourceModload(t, runner, driver, src)
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			body := emittedBody(t, string(asm), "__fn_"+name)
			if strings.Contains(body, "call *") || strings.Contains(body, "callq *") {
				t.Fatalf("resolved fused callback still dispatches indirectly:\n%s", body)
			}
			if name != "via_capture" && strings.Contains(body, "call __fn_"+name+"$wrap") {
				t.Fatalf("tiny capture-free callback still calls its wrapper:\n%s", body)
			}
			// This fixture's callbacks use i64. The only i32 arithmetic is
			// the fused cursor, whose increment is dominated by i < len.
			if name == "via_map_fold" && strings.Contains(body, "movslq ") {
				t.Fatalf("bounded fused cursor still sign-extends each increment:\n%s", body)
			}
			if name == "via_map_fold" && strings.Contains(body, "__fern_arr_dec") {
				t.Fatalf("inlined scalar callbacks still retain closure cleanup:\n%s", body)
			}
			if name == "via_map_map_reduce" || name == "via_order_sensitive" {
				assertSeededReductionLoop(t, body)
			}
		})
	}
	bin := buildBin(t, gcc, dir, "fusion-dispatch", asm)
	if out, err := runX86_64Bin(runner, bin).CombinedOutput(); err != nil {
		t.Fatalf("fused direct callbacks: %v\n%s", err, out)
	}
}

// The map-only reduction must have one loop with only its bound test. A
// per-element first-arrival flag adds a second conditional branch; a call to
// the unfused combinator leaves no loop here. Check both to avoid a vacuous pass.
func assertSeededReductionLoop(t *testing.T, body string) {
	t.Helper()
	lines := strings.Split(body, "\n")
	labels := map[string]int{}
	loops := 0
	for i, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasSuffix(line, ":") {
			labels[strings.TrimSuffix(line, ":")] = i
		}
		fields := strings.Fields(line)
		if len(fields) != 2 || !strings.HasPrefix(fields[0], "j") {
			continue
		}
		start, backward := labels[fields[1]]
		if !backward {
			continue
		}
		loops++
		tests := 0
		for _, instruction := range lines[start : i+1] {
			op := strings.Fields(instruction)
			if len(op) == 2 && strings.HasPrefix(op[0], "j") && op[0] != "jmp" {
				tests++
			}
		}
		if tests != 1 {
			t.Fatalf("seeded reduction loop has %d conditional branches, want only the bound test:\n%s", tests, body)
		}
	}
	if loops != 1 {
		t.Fatalf("seeded reduction has %d emitted loops, want one:\n%s", loops, body)
	}
}
