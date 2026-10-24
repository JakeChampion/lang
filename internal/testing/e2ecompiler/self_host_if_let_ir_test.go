package e2ecompiler

import (
	"os/exec"
	"strings"
	"testing"
)

var ifLetIRCases = []struct {
	name          string
	src           string
	exit          int
	staticReclaim bool
}{
	{"some", "import \"core/map\"; struct Point { x: i32, y: i32 } function main(): i32 { let t: Point = Point { x: 1, y: 1 }; let pad: i32 = t.x - t.y; let m: Map[string,i32] = map_new(4); m = m.insert(\"k\", 42); if let Some(v) = m.get(\"k\") { return v + pad; } else { return 1 + pad; } }", 42, true},
	{"none-else", "import \"core/map\"; struct Point { x: i32, y: i32 } function main(): i32 { let t: Point = Point { x: 1, y: 1 }; let pad: i32 = t.x - t.y; let m: Map[string,i32] = map_new(4); m = m.insert(\"k\", 42); if let Some(v) = m.get(\"absent\") { return v + pad; } else { return 7 + pad; } }", 7, true},
	{"no-else-fallthrough", "import \"core/map\"; struct Point { x: i32, y: i32 } function main(): i32 { let t: Point = Point { x: 1, y: 1 }; let pad: i32 = t.x - t.y; let m: Map[string,i32] = map_new(4); m = m.insert(\"k\", 5); if let Some(v) = m.get(\"absent\") { return v + pad; } return 9 + pad; }", 9, true},
	{"user-variant", "enum Shape { Circle(i32), Empty } struct Point { x: i32, y: i32 } function main(): i32 { let t: Point = Point { x: 1, y: 1 }; let pad: i32 = t.x - t.y; let s: Shape = Circle(42); if let Circle(r) = s { return r + pad; } else { return 0 + pad; } }", 42, false},
	{"labeled-then-block", "enum Shape { Circle(i32), Empty } struct Point { x: i32, y: i32 } function main(): i32 { let t: Point = Point { x: 1, y: 1 }; let pad: i32 = t.x - t.y; let s: Shape = Circle(42); if let Circle(r) = s { lp: while (true) { return r + pad; } } return 0 + pad; }", 42, false},
	{"or-first-alt", "enum E { A(i32), B(i32), C(i32) } struct Point { x: i32, y: i32 } function main(): i32 { let t: Point = Point { x: 1, y: 1 }; let pad: i32 = t.x - t.y; let e: E = A(41); if let A(v) | B(v) = e { return v + pad; } return 1 + pad; }", 41, false},
	{"or-second-alt", "enum E { A(i32), B(i32), C(i32) } struct Point { x: i32, y: i32 } function main(): i32 { let t: Point = Point { x: 1, y: 1 }; let pad: i32 = t.x - t.y; let e: E = B(42); if let A(v) | B(v) = e { return v + pad; } return 1 + pad; }", 42, false},
	{"or-no-match-else", "enum E { A(i32), B(i32), C(i32) } struct Point { x: i32, y: i32 } function main(): i32 { let t: Point = Point { x: 1, y: 1 }; let pad: i32 = t.x - t.y; let e: E = C(9); if let A(v) | B(v) = e { return v + pad; } else { return 7 + pad; } }", 7, false},
	{"or-three-alts", "enum E { A(i32), B(i32), C(i32) } struct Point { x: i32, y: i32 } function main(): i32 { let t: Point = Point { x: 1, y: 1 }; let pad: i32 = t.x - t.y; let e: E = C(13); if let A(v) | B(v) | C(v) = e { return v + pad; } return 1 + pad; }", 13, false},
	{"struct-shorthand", "struct Point { x: i32, y: i32 } function main(): i32 { let t: Point = Point { x: 1, y: 1 }; let pad: i32 = t.x - t.y; let p: Point = Point { x: 3, y: 4 }; if let Point { x, y } = p { return x + y + pad; } return 1 + pad; }", 7, false},
	{"struct-with-else", "struct Point { x: i32, y: i32 } function main(): i32 { let t: Point = Point { x: 1, y: 1 }; let pad: i32 = t.x - t.y; let p: Point = Point { x: 3, y: 4 }; if let Point { x, y } = p { return x + y + pad; } else { return 99 + pad; } }", 7, false},
	{"struct-rename", "struct Point { x: i32, y: i32 } function main(): i32 { let t: Point = Point { x: 1, y: 1 }; let pad: i32 = t.x - t.y; let p: Point = Point { x: 3, y: 4 }; if let Point { x: a, y: b } = p { return a * 10 + b + pad; } return 1 + pad; }", 34, false},
	{"struct-rest", "struct P3 { x: i32, y: i32, z: i32 } struct Point { x: i32, y: i32 } function main(): i32 { let t: Point = Point { x: 1, y: 1 }; let pad: i32 = t.x - t.y; let p: P3 = P3 { x: 3, y: 4, z: 5 }; if let P3 { x, .. } = p { return x + pad; } return 1 + pad; }", 3, false},
	{"struct-at-binding", "struct Point { x: i32, y: i32 } function main(): i32 { let t: Point = Point { x: 1, y: 1 }; let pad: i32 = t.x - t.y; let p: Point = Point { x: 3, y: 4 }; if let w @ Point { x, y } = p { return x + y + w.x + pad; } return 1 + pad; }", 10, false},
}

func ifLetRuntimeSource(t *testing.T, src string) string {
	t.Helper()
	for _, required := range []string{"function main(): i32 {", "Point { x: 1, y: 1 }"} {
		if strings.Count(src, required) != 1 {
			t.Fatalf("runtime fixture needs exactly one %q", required)
		}
	}
	return strings.NewReplacer(
		"function main(): i32 {", "@noinline function probe(n: i32): i32 {",
		"Point { x: 1, y: 1 }", "Point { x: n, y: n }",
		"Point { x: 3, y: 4 }", "Point { x: n, y: n + 1 }",
		"P3 { x: 3, y: 4, z: 5 }", "P3 { x: n, y: n + 1, z: n + 2 }",
		`m.insert("k", 42)`, `m.insert("k", n + 39)`,
		`m.insert("k", 5)`, `m.insert("k", n + 2)`,
		"Circle(42)", "Circle(n + 39)",
		"A(41)", "A(n + 38)",
		"B(42)", "B(n + 39)",
		"C(9)", "C(n + 6)",
		"C(13)", "C(n + 10)",
	).Replace(src) + " function main(): i32 { return probe(args().len()); }"
}

func TestSelfHostIfLetIRX86_64(t *testing.T) {
	boxedProbes(t)
	gcc, runner := x86_64Tooling(t)
	l := newStdlibLoader(t)
	dir := t.TempDir()

	for _, tc := range ifLetIRCases {
		for _, variant := range []struct {
			name, src string
			heap      bool
		}{
			{"original", tc.src, false}, {"runtime", ifLetRuntimeSource(t, tc.src), true},
		} {
			t.Run(tc.name+"/"+variant.name, func(t *testing.T) {
				asm := []byte(l.emit(t, variant.src))
				if len(asm) == 0 {
					t.Fatal("self-host compiler emitted 0 bytes")
				}
				if variant.heap {
					body := selfHostFnBody(t, asm, "probe")
					for _, call := range []string{"call __fern_arr_box", "call __fn___fern_arr_dec"} {
						if !strings.Contains(body, call) {
							t.Errorf("probe missing %s:\n%s", call, body)
						}
					}
				} else if got := strings.Contains(selfHostFnBody(t, asm, "main"), "call __fn___fern_arr_dec"); got != tc.staticReclaim {
					t.Errorf("original reclaim call = %v, want %v:\n%s", got, tc.staticReclaim, asm)
				}
				progBin := buildBin(t, gcc, dir, tc.name, string(asm))
				var cmd *exec.Cmd
				if len(runner) == 0 {
					cmd = exec.Command(progBin, "a", "b")
				} else {
					cmd = exec.Command(runner[0], append(runner[1:], progBin, "a", "b")...)
				}
				_ = cmd.Run()
				if code := cmd.ProcessState.ExitCode(); code != tc.exit {
					t.Errorf("%s exited %d, want %d", tc.name, code, tc.exit)
				}
			})
		}
	}
}
