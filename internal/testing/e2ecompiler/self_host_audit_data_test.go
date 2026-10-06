package e2ecompiler

import (
	"testing"
)

// auditDataCases isolate string / array / map features and run them
// through the SELF-HOSTED compiler, asserting the exit code. Self-host
// arm of the §A / §B / §C audit (docs/FEATURE-AUDIT.md); the native arm
// is the `audit_strings_arrays_maps` fixture (all four native backends).
//
// `.with` uses the canonical reassignment idiom (`a = a.with(i, v)`) —
// reading the pre-`.with` binding diverges across backends (#2832).
var auditDataCases = []struct {
	name string
	src  string
	exit int
}{
	// strings
	{"string-len", `function main(): i32 { let s: string = "hello"; return s.len(); }`, 5},
	{"string-concat", `function main(): i32 { let s: string = "ab" + "cde"; return s.len(); }`, 5},
	{"string-eq", `function main(): i32 { if ("abc" == "abc" && "abc" != "abd") { return 5; } return 0; }`, 5},
	{"string-index", `function main(): i32 { let s: string = "ABC"; return (s[0] as i32) + (s[2] as i32); }`, 132},
	{"string-slice", `function main(): i32 { let s: string = "hello"; let t: string = slice_unchecked(s, 1, 4) + ""; return t.len(); }`, 3},
	// arrays
	{"array-literal-index", `function main(): i32 { let a: i32[] = [10, 20, 30]; return a[0] + a[2]; }`, 40},
	{"array-len", `function main(): i32 { let a: i32[] = [1, 2, 3, 4]; return a.len(); }`, 4},
	{"array-with", `function main(): i32 { let a: i32[] = [1, 2, 3]; a = a.with(1, 20); return a[0] + a[1] + a[2]; }`, 24},
	{"array-foreach", `function main(): i32 { let a: i32[] = [2, 3, 4]; let s: i32 = 0; for x in a { s = s + x; } return s; }`, 9},
	// maps
	{"map-i32-insert-getor", `import "core/map";
function main(): i32 { let m: Map[i32,i32] = map_new(8); m = m.insert(1,10); m = m.insert(2,20); m = m.insert(1,99); return m.get_or(1,0) + m.get_or(2,0); }`, 119},
	{"map-has", `import "core/map";
function main(): i32 { let m: Map[i32,i32] = map_new(8); m = m.insert(5,1); if (m.has(5) && !m.has(9)) { return 7; } return 0; }`, 7},
	{"map-string-keys", `import "core/map";
function main(): i32 { let m: Map[string,i32] = map_new(8); m = m.insert("a",3); m = m.insert("b",4); return m.get_or("a",0) + m.get_or("b",0); }`, 7},
	{"map-len", `import "core/map";
function main(): i32 { let m: Map[i32,i32] = map_new(8); m = m.insert(1,1); m = m.insert(2,2); m = m.insert(3,3); return m.len(); }`, 3},
}

// TestSelfHostAuditDataX86_64 runs each string/array/map case through the
// self-hosted x86-64 load driver and asserts the exit code.
func TestSelfHostAuditDataX86_64(t *testing.T) {
	l := newStdlibLoader(t)
	for _, tc := range auditDataCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := l.emit(t, tc.src)
			cmd := runX86_64Bin(l.runner, buildBin(t, l.gcc, t.TempDir(), tc.name, asm))
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.exit)
			}
		})
	}
}

// TestSelfHostAuditDataArm64 — CI-gated arm64 counterpart.
func TestSelfHostAuditDataArm64(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, tc := range auditDataCases {
		t.Run(tc.name, func(t *testing.T) {
			src := tc.src
			for _, target := range []string{"arm64-linux"} {
				if stderr, code := cli.exitOf(t, src, target); code != tc.exit {
					t.Errorf("%s on %s exited %d, want %d\n%s", tc.name, target, code, tc.exit, stderr)
				}
			}
		})
	}
}
