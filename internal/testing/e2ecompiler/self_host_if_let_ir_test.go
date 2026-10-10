package e2ecompiler

import (
	"bytes"
	"os/exec"
	"strings"
	"testing"
)

// ifLetIRCases pin `if let PAT = EXPR { then } else { else }` on the IR path.
// The parser desugars `if let` to `match (EXPR) { PAT => { then }, _ => { else } }`
// (parser.fern, s_match_origin origin "if_let"), so it uses the match IR
// machinery, mirroring self_host_bool_match_ir_test.go.
//
// Original constant aggregates use static storage; map cases still reclaim
// runtime values. Paired input-driven programs exercise heap reclamation while
// preserving every pattern and exit oracle. The loader is IR-or-error, so a
// reclaim call is not needed to establish IR admission.
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
	// if-let source swallowed as struct literal (#4339 item 4): the source
	// expression parses with struct literals suppressed (native's
	// noStructLit), so a bare-ident source whose then-block opens with a
	// labeled loop (`lp: while …` — the IDENT `:` struct-lit lookahead) is
	// no longer taken as `s { … }`. Pre-fix the whole statement mis-parsed and
	// the program mis-ran.
	{"labeled-then-block", "enum Shape { Circle(i32), Empty } struct Point { x: i32, y: i32 } function main(): i32 { let t: Point = Point { x: 1, y: 1 }; let pad: i32 = t.x - t.y; let s: Shape = Circle(42); if let Circle(r) = s { lp: while (true) { return r + pad; } } return 0 + pad; }", 42, false},
	// Or-patterns in the head: `if let A(x) | B(x) = e`. Each alternative
	// becomes its own arm sharing the then-block, the same per-alternative
	// expansion match arms use, so the binding is live whichever matched.
	// Before this the `|` was left on the cursor and the statement mis-parsed
	// into a P001 pair ("missing { in block", then a bogus assign).
	{"or-first-alt", "enum E { A(i32), B(i32), C(i32) } struct Point { x: i32, y: i32 } function main(): i32 { let t: Point = Point { x: 1, y: 1 }; let pad: i32 = t.x - t.y; let e: E = A(41); if let A(v) | B(v) = e { return v + pad; } return 1 + pad; }", 41, false},
	{"or-second-alt", "enum E { A(i32), B(i32), C(i32) } struct Point { x: i32, y: i32 } function main(): i32 { let t: Point = Point { x: 1, y: 1 }; let pad: i32 = t.x - t.y; let e: E = B(42); if let A(v) | B(v) = e { return v + pad; } return 1 + pad; }", 42, false},
	// A variant outside the alternatives takes the else — the trailing
	// wildcard arm still has to be appended after the expansion.
	{"or-no-match-else", "enum E { A(i32), B(i32), C(i32) } struct Point { x: i32, y: i32 } function main(): i32 { let t: Point = Point { x: 1, y: 1 }; let pad: i32 = t.x - t.y; let e: E = C(9); if let A(v) | B(v) = e { return v + pad; } else { return 7 + pad; } }", 7, false},
	{"or-three-alts", "enum E { A(i32), B(i32), C(i32) } struct Point { x: i32, y: i32 } function main(): i32 { let t: Point = Point { x: 1, y: 1 }; let pad: i32 = t.x - t.y; let e: E = C(13); if let A(v) | B(v) | C(v) = e { return v + pad; } return 1 + pad; }", 13, false},
	// Struct-pattern heads. These route to build_struct_match — the same
	// field-bind flag chain the match arms use — rather than parse_pattern,
	// whose `IDENT {` is the record-form VARIANT pattern instead. `structnames`
	// tells the two apart. The pattern is irrefutable, so the `with-else` case
	// pins that a written else parses and stays unreachable.
	{"struct-shorthand", "struct Point { x: i32, y: i32 } function main(): i32 { let t: Point = Point { x: 1, y: 1 }; let pad: i32 = t.x - t.y; let p: Point = Point { x: 3, y: 4 }; if let Point { x, y } = p { return x + y + pad; } return 1 + pad; }", 7, false},
	{"struct-with-else", "struct Point { x: i32, y: i32 } function main(): i32 { let t: Point = Point { x: 1, y: 1 }; let pad: i32 = t.x - t.y; let p: Point = Point { x: 3, y: 4 }; if let Point { x, y } = p { return x + y + pad; } else { return 99 + pad; } }", 7, false},
	// `field: local` renaming, which the shared named-field parser handles.
	{"struct-rename", "struct Point { x: i32, y: i32 } function main(): i32 { let t: Point = Point { x: 1, y: 1 }; let pad: i32 = t.x - t.y; let p: Point = Point { x: 3, y: 4 }; if let Point { x: a, y: b } = p { return a * 10 + b + pad; } return 1 + pad; }", 34, false},
	// `..` rest: bind one field, ignore the remainder.
	{"struct-rest", "struct P3 { x: i32, y: i32, z: i32 } struct Point { x: i32, y: i32 } function main(): i32 { let t: Point = Point { x: 1, y: 1 }; let pad: i32 = t.x - t.y; let p: P3 = P3 { x: 3, y: 4, z: 5 }; if let P3 { x, .. } = p { return x + pad; } return 1 + pad; }", 3, false},
	// `@` binding the whole struct alongside its destructured fields.
	{"struct-at-binding", "struct Point { x: i32, y: i32 } function main(): i32 { let t: Point = Point { x: 1, y: 1 }; let pad: i32 = t.x - t.y; let p: Point = Point { x: 3, y: 4 }; if let w @ Point { x, y } = p { return x + y + w.x + pad; } return 1 + pad; }", 10, false},
}

// ifLetRuntimeSource retains the original control flow and patterns, replacing
// aggregate payloads with a noinline function's runtime input. With n=3 every
// matched payload and exit code remains the same as in the original program.
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

// TestSelfHostIfLetIRX86_64 checks original and runtime aggregate representations
// through the self-hosted IR-or-error load driver and executes every exit oracle.
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
				} else if got := bytes.Contains(asm, []byte("call __fn___fern_arr_dec")); got != tc.staticReclaim {
					t.Errorf("original reclaim call = %v, want %v:\n%s", got, tc.staticReclaim, asm)
				}
				progBin := buildBin(t, gcc, dir, tc.name, string(asm))
				var cmd *exec.Cmd
				// args includes the program name, giving probe n=3.
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
