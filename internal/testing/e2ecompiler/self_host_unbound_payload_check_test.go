package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// A built-in variant's type argument its constructor did not bind (`E` in an
// un-annotated `Ok(5)`) is a payload the value never holds (#12788), so the
// `Err(e)` arm is unreachable and `e` fits an `i32[]`, an `(i32, i32)`, an
// `i32` variant payload or a `Map[string, i32]` alike, in both checkers.
// Found by TestDifferential_CheckerMutants.
func TestSelfHostUnboundPayloadCheck(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("the CLI driver takes host filesystem paths as argv")
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")
	nativeBin := buildFernCLIBin(t)
	stdlib, err := filepath.Abs(filepath.Join("..", "..", "stdlib"))
	if err != nil {
		t.Fatalf("stdlib path: %v", err)
	}
	cases := []struct {
		name, src string
		ok        bool
	}{
		{"unbound payload in an array", "function main(): i32 {\n    let r = Ok(5);\n    match (r) { Ok(a) => { return a; }, Err(e) => { let xs: i32[] = [e, 1]; return xs[1]; } }\n}\n", true},
		{"bound payload in an array", "function main(): i32 {\n    let r: Result[i32, i32] = Ok(5);\n    match (r) { Ok(a) => { return a; }, Err(e) => { let xs: i32[] = [e, 1]; return xs[1]; } }\n}\n", true},
		{"unbound payload in a tuple", "function main(): i32 {\n    let r = Ok(5);\n    match (r) { Ok(a) => { return a; }, Err(e) => { let p: (i32, i32) = (e, 1); return p.1; } }\n}\n", true},
		{"bound payload in a tuple", "function main(): i32 {\n    let r: Result[i32, i32] = Ok(5);\n    match (r) { Ok(a) => { return a; }, Err(e) => { let p: (i32, i32) = (e, 1); return p.1; } }\n}\n", true},
		{"unbound payload in a variant", "enum W { Wrap(i32), Nil }\nfunction main(): i32 {\n    let r = Ok(5);\n    match (r) { Ok(a) => { return a; }, Err(e) => { let w: W = Wrap(e); match (w) { Wrap(x) => { return x; }, Nil => { return 0; } } } }\n}\n", true},
		{"bound payload in a variant", "enum W { Wrap(i32), Nil }\nfunction main(): i32 {\n    let r: Result[i32, i32] = Ok(5);\n    match (r) { Ok(a) => { return a; }, Err(e) => { let w: W = Wrap(e); match (w) { Wrap(x) => { return x; }, Nil => { return 0; } } } }\n}\n", true},
		{"unbound payload in a map", "import \"core/map\";\nfunction main(): i32 {\n    let r = Ok(5);\n    match (r) { Ok(a) => { return a; }, Err(e) => { let m: Map[string, i32] = Map { \"a\": e }; return m.len(); } }\n}\n", true},
		{"bound payload in a map", "import \"core/map\";\nfunction main(): i32 {\n    let r: Result[i32, i32] = Ok(5);\n    match (r) { Ok(a) => { return a; }, Err(e) => { let m: Map[string, i32] = Map { \"a\": e }; return m.len(); } }\n}\n", true},
		{"bound payload of another type in an array", "function main(): i32 {\n    let r: Result[i32, string] = Ok(5);\n    match (r) { Ok(a) => { return a; }, Err(e) => { let xs: i32[] = [e, 1]; return xs[1]; } }\n}\n", false},
		{"generic result bound by its destination", "function mk[T](): T[] { return []; }\nfunction main(): i32 {\n    let ys: i32[] = mk();\n    return ys.len();\n}\n", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := filepath.Join(t.TempDir(), "main.fern")
			if err := os.WriteFile(src, []byte(tc.src), 0o644); err != nil {
				t.Fatal(err)
			}
			nativeOut, nativeErr := exec.Command(nativeBin, "-check", src).CombinedOutput()
			shOut, shErr := exec.Command(driverBin, "-check", src, stdlib).CombinedOutput()
			if (nativeErr == nil) != tc.ok {
				t.Errorf("Go checker accepts = %v, want %v\n%s", nativeErr == nil, tc.ok, nativeOut)
			}
			if (shErr == nil) != tc.ok {
				t.Errorf("self-host checker accepts = %v, want %v\n%s", shErr == nil, tc.ok, shOut)
			}
		})
	}
}
