package e2ecompiler

import (
	"fmt"
	"strings"
	"testing"
)

func largeOffsetLiteralSource(kind string, count int) string {
	var s strings.Builder
	if kind == "record" {
		s.WriteString("struct Entry { value: i64, tag: i64 }\n")
	}
	element, seed := "Entry", "i64"
	if kind == "float" {
		element, seed = "f64", "f64"
	}
	fmt.Fprintf(&s, "function values(seed: %s): %s[] { return [", seed, element)
	for i := 0; i < count; i++ {
		if kind == "record" {
			fmt.Fprintf(&s, "Entry { value: seed + %d as i64, tag: %d as i64 },", i, i%97)
		} else {
			fmt.Fprintf(&s, "seed + %d.0,", i)
		}
	}
	fmt.Fprintf(&s, "]; }\nfunction main(): i32 { let seed = __heap_bump_bytes() as %s; let a = values(seed); if (a.len() != %d) { return 98; } let i = 0; while (i < a.len()) {", seed, count)
	if kind == "record" {
		s.WriteString("if (a[i].value != seed + i as i64 || a[i].tag != (i % 97) as i64) { return 99; }")
	} else {
		s.WriteString("if (a[i] != seed + i as f64) { return 99; }")
	}
	s.WriteString("i = i + 1; }")
	for _, i := range []int{0, 2047, 2048, 4094, 4095, 4096, 8190, 8191, count - 1} {
		if i >= count {
			continue
		}
		if kind == "record" {
			fmt.Fprintf(&s, "if (a[%d].value != seed + %d as i64 || a[%d].tag != %d as i64) { return 97; }", i, i, i, i%97)
		} else {
			fmt.Fprintf(&s, "if (a[%d] != seed + %d.0) { return 97; }", i, i)
		}
	}
	s.WriteString("return 0; }")
	return s.String()
}

func TestSelfHostArm64LargeMemoryOffsets(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, kind := range []string{"record", "float"} {
		for _, count := range []int{4095, 4100, 8193} {
			t.Run(fmt.Sprintf("%s-%d", kind, count), func(t *testing.T) {
				source := largeOffsetLiteralSource(kind, count)
				for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
					t.Run(target, func(t *testing.T) {
						stderr, code := cli.exitOf(t, source, target, "FERN_STRICT_IR=1", "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
						if code != 0 {
							t.Fatalf("large literal exited %d: %s", code, stderr)
						}
						assertBalancedCensus(t, stderr)
					})
				}
			})
		}
	}
}
