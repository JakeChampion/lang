package e2ecompiler

import (
	"os/exec"
	"strings"
	"testing"
)

var letElseIRCases = []struct {
	name          string
	src           string
	exit          int
	staticReclaim bool
}{
	{"matched", "enum Shape { Circle(i32), Empty } struct Point { x: i32, y: i32 } function main(): i32 { let t: Point = Point { x: 1, y: 1 }; let pad: i32 = t.x - t.y; let s: Shape = Circle(42); let Circle(r) = s else { return 0 + pad; }; return r + pad; }", 42, false},
	{"else-path", "enum Shape { Circle(i32), Empty } struct Point { x: i32, y: i32 } function main(): i32 { let t: Point = Point { x: 1, y: 1 }; let pad: i32 = t.x - t.y; let s: Shape = Empty; let Circle(r) = s else { return 7 + pad; }; return r + pad; }", 7, false},
	{"opt-some", "import \"core/map\"; struct Point { x: i32, y: i32 } function main(): i32 { let t: Point = Point { x: 1, y: 1 }; let pad: i32 = t.x - t.y; let m: Map[string,i32] = map_new(4); m = m.insert(\"k\", 42); let Some(v) = m.get(\"k\") else { return 1 + pad; }; return v + pad; }", 42, true},
	{"opt-none", "import \"core/map\"; struct Point { x: i32, y: i32 } function main(): i32 { let t: Point = Point { x: 1, y: 1 }; let pad: i32 = t.x - t.y; let m: Map[string,i32] = map_new(4); m = m.insert(\"k\", 42); let Some(v) = m.get(\"absent\") else { return 9 + pad; }; return v + pad; }", 9, true},
	{"rest-multi", "import \"core/map\"; struct Point { x: i32, y: i32 } function main(): i32 { let t: Point = Point { x: 1, y: 1 }; let pad: i32 = t.x - t.y; let m: Map[string,i32] = map_new(4); m = m.insert(\"k\", 40); let Some(v) = m.get(\"k\") else { return 1 + pad; }; let w: i32 = v + 2 + pad; return w; }", 42, true},
	{"at-binding", "enum E { A(i32), B(i32) } struct Point { x: i32, y: i32 } function whole(e: E): i32 { match (e) { A(v) => { return v; }, B(v) => { return 0; } } } function main(): i32 { let t: Point = Point { x: 1, y: 1 }; let pad: i32 = t.x - t.y; let e: E = A(6); let w @ A(x) = e else { return 1 + pad; }; return whole(w) + x + pad; }", 12, false},
	{"or-first-alt", "enum E { A(i32), B(i32), C(i32) } struct Point { x: i32, y: i32 } function main(): i32 { let t: Point = Point { x: 1, y: 1 }; let pad: i32 = t.x - t.y; let e: E = A(4); let A(x) | B(x) = e else { return 21 + pad; }; return x + pad; }", 4, false},
	{"or-second-alt", "enum E { A(i32), B(i32), C(i32) } struct Point { x: i32, y: i32 } function main(): i32 { let t: Point = Point { x: 1, y: 1 }; let pad: i32 = t.x - t.y; let e: E = B(4); let A(x) | B(x) = e else { return 21 + pad; }; return x + pad; }", 4, false},
	{"or-no-match-else", "enum E { A(i32), B(i32), C(i32) } struct Point { x: i32, y: i32 } function main(): i32 { let t: Point = Point { x: 1, y: 1 }; let pad: i32 = t.x - t.y; let e: E = C(9); let A(x) | B(x) = e else { return 21 + pad; }; return x + pad; }", 21, false},
}

func letElseRuntimeSource(t *testing.T, src string) string {
	t.Helper()
	for _, required := range []string{"function main(): i32 {", "Point { x: 1, y: 1 }"} {
		if strings.Count(src, required) != 1 {
			t.Fatalf("runtime fixture needs exactly one %q", required)
		}
	}
	return strings.NewReplacer(
		"function main(): i32 {", "@noinline function probe(n: i32): i32 {",
		"Point { x: 1, y: 1 }", "Point { x: n, y: n }",
		`m.insert("k", 42)`, `m.insert("k", n + 39)`,
		`m.insert("k", 40)`, `m.insert("k", n + 37)`,
		"Circle(42)", "Circle(n + 39)",
		"A(6)", "A(n + 3)",
		"A(4)", "A(n + 1)",
		"B(4)", "B(n + 1)",
		"C(9)", "C(n + 6)",
	).Replace(src) + " function main(): i32 { return probe(args().len()); }"
}

func TestSelfHostLetElseIRX86_64(t *testing.T) {
	boxedProbes(t)
	gcc, runner := x86_64Tooling(t)
	l := newStdlibLoader(t)
	dir := t.TempDir()

	for _, tc := range letElseIRCases {
		for _, variant := range []struct {
			name, src string
			heap      bool
		}{
			{"original", tc.src, false}, {"runtime", letElseRuntimeSource(t, tc.src), true},
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
