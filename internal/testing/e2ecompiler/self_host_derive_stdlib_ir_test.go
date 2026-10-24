package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var deriveStdlibCases = []struct {
	name string
	src  string
}{
	{"hash-struct", `import "core/cmp";
@derive(cmp.Hash)
struct P { x: i32, y: i32 }
function main(): i32 { let a = P { x: 3, y: 5 }; let b = P { x: 3, y: 5 }; let c = P { x: 3, y: 6 }; if (a.hash() == b.hash() && a.hash() != c.hash()) { return 42; } return 0; }`},
	{"hash-string-field", `import "core/cmp";
@derive(cmp.Hash)
struct S { name: string, n: i32 }
function main(): i32 { let a = S { name: "hi", n: 1 }; let b = S { name: "hi", n: 1 }; if (a.hash() == b.hash()) { return 7; } return 0; }`},
	{"json-struct", `import "std/json";
@derive(json.Json)
struct P { x: i32, name: string }
function main(): i32 { let a = P { x: 7, name: "hi" }; return a.to_json().len(); }`},
	{"json-from-struct", `import "std/json";
@derive(json.FromJson) struct User { id: i32, name: string }
function main(): i32 {
    let sum: i32 = 0;
    match (User.from_json("{\"id\":9,\"name\":\"x\"}")) { Ok(u) => { sum = sum + u.id; }, Err(e) => { sum = sum + 100; } }
    match (User.from_json("{\"id\":1}")) { Ok(u2) => { sum = sum + 100; }, Err(e) => { sum = sum + 7; } }
    match (User.from_json("nope")) { Ok(u3) => { sum = sum + 100; }, Err(e) => { sum = sum + 5; } }
    return sum;
}`},
	{"json-from-struct-scalar-modules-imported", `import "std/json";
import "std/i32";
import "std/string";
@derive(json.FromJson) struct User { id: i32, name: string }
function main(): i32 {
    match (User.from_json("{\"id\":9,\"name\":\"xy\"}")) { Ok(u) => { return u.id + u.name.len(); }, Err(e) => { return 100; } }
}`},
	{"json-from-bool", `import "std/json";
@derive(json.FromJson) struct Flag { id: i32, on: boolean }
function main(): i32 {
    match (Flag.from_json("{\"id\":4,\"on\":true}")) { Ok(f) => { if (f.on) { return f.id + 10; } return f.id; }, Err(e) => { return 1; } }
}`},
	{"json-roundtrip-bool", `import "std/json";
@derive(json.Json, json.FromJson) struct Flag { id: i32, on: boolean, off: boolean }
function main(): i32 {
    let f = Flag { id: 5, on: true, off: false };
    match (Flag.from_json(f.to_json())) { Ok(g) => { let r = g.id; if (g.on) { r = r + 10; } if (!g.off) { r = r + 100; } return r; }, Err(e) => { return 1; } }
}`},
	{"json-from-var-try", `import "std/json";
@derive(json.FromJson) struct User { id: i32, name: string }
function pick(s: string): Result[i32, string] { let u: User = User.from_json(s)?; return Ok(u.id); }
function main(): i32 {
    let r = User.from_json("{\"id\":8,\"name\":\"y\"}");
    let a = 0;
    match (r) { Ok(u) => { a = u.id; }, Err(e) => { a = 100; } }
    match (pick("{\"id\":3,\"name\":\"z\"}")) { Ok(n) => { a = a + n; }, Err(e) => { a = a + 100; } }
    return a;
}`},
	{"json-roundtrip", `import "std/json";
@derive(json.Json, json.FromJson) struct P { x: i32, name: string }
function main(): i32 {
    let p = P { x: 7, name: "hi" };
    match (P.from_json(p.to_json())) { Ok(q) => { return q.x; }, Err(e) => { return 1; } }
}`},
	{"ord-string-key", `import "core/cmp";
@derive(cmp.Ord)
struct S { name: string, id: i32 }
function main(): i32 { let a = S { name: "abc", id: 1 }; let b = S { name: "abd", id: 1 }; if (a.cmp(b) < 0 && b.cmp(a) > 0 && a.cmp(a) == 0) { return 42; } return 0; }`},
	{"combined-all", `import "core/cmp";
import "std/json";
@derive(cmp.Eq, cmp.Ord, cmp.Hash, json.Json)
struct Rec { name: string, id: i32 }
function main(): i32 { let a = Rec { name: "x", id: 1 }; let b = Rec { name: "y", id: 2 }; let n = 0; if (a.eq(a)) { n = n + 1; } if (a.cmp(b) < 0) { n = n + 1; } if (a.hash() != b.hash()) { n = n + 1; } if (a.to_json().len() > 0) { n = n + 1; } return n; }`},
}

func TestSelfHostDeriveStdlibIR(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := copySelfHostTree(t)
	driver := buildSelfHostBin(t, gcc, dir, "drivers/asm_load_run.fern", "alr")
	root, err := filepath.Abs("../../stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}

	runDriver := func(args ...string) (string, int) {
		argv := append([]string{driver}, args...)
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(argv[0], argv[1:]...)
		} else {
			cmd = exec.Command(runner[0], append(runner[1:], argv...)...)
		}
		out, _ := cmd.Output()
		return string(out), cmd.ProcessState.ExitCode()
	}

	for _, tc := range deriveStdlibCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			entry := filepath.Join(dir, "ds_"+tc.name+".fern")
			if err := os.WriteFile(entry, []byte(tc.src+"\n"), 0o644); err != nil {
				t.Fatalf("write entry: %v", err)
			}
			_, want := runFixtureInterp(t, entry, "")
			if out, _ := runDriver(entry, root, "-decide"); strings.TrimSpace(out) != "ir" {
				t.Errorf("%s decide = %q, want \"ir\"", tc.name, strings.TrimSpace(out))
			}
			asm, _ := runDriver(entry, root)
			if len(asm) == 0 {
				t.Fatalf("%s: driver emitted 0 bytes", tc.name)
			}
			bin := buildBin(t, gcc, dir, "ds_"+tc.name+"_bin", asm)
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(bin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], bin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != want {
				t.Errorf("%s self-host run = %d, want %d (native oracle)", tc.name, code, want)
			}
		})
	}
}
