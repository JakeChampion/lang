package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// Keep the fallback's examples paired with programs that still need it.
// When the checker learns one of them, update the diagnostic as well as the
// expected exit code. The other rows guard previously unsupported categories.
func TestSelfHostCheckerFallbackHint(t *testing.T) {
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "fern.fern")
	var driver string
	var runner []string
	if runtime.GOOS == "darwin" && runtime.GOARCH == "arm64" {
		driver = buildSelfHostBinArm64Darwin(t, dir, "fern.fern", "fern")
	} else {
		var gcc string
		gcc, runner = x86_64Tooling(t)
		driver = buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")
	}
	native := buildLangBinForInterp(t)
	stdlibRoot, err := filepath.Abs("../../internal/stdlib")
	if err != nil {
		t.Fatal(err)
	}
	const dynPrelude = `trait Greet { function hi(self: Self): i32; }
struct Dog {}
impl Greet for Dog { function hi(self: Self): i32 { return 7; } }
`
	// fallback is the line the hint names: the innermost statement the
	// checker could not type, or 0 for a program it accepts.
	for _, tc := range []struct {
		name     string
		source   string
		fallback int
	}{
		{"enum", `enum Color { Red, Blue } function main(): i32 { var c: Color = Red; return match (c) { Red => 0, Blue => 1 }; }`, 0},
		{"generic", `function ident[T](v: T): T { return v; } function main(): i32 { return ident(0); }`, 0},
		{"option", `function main(): i32 { var o: Option[i32] = Some(3); return match (o) { Some(n) => n, None => 0 }; }`, 0},
		{"result", `function main(): i32 { var r: Result[i32, string] = Ok(3); return match (r) { Ok(n) => n, Err(_) => 0 }; }`, 0},
		// A match STATEMENT on a built-in enum: Option's and Result's variants
		// have no struct sig, and JsonValue is in no union table.
		{"option-match-stmt", `function f(o: Option[i32]): i32 { match (o) { Some(n) => { return n; }, None => { return 0; } } } function main(): i32 { return f(Some(3)); }`, 0},
		{"result-match-stmt", `function f(r: Result[i32, string]): i32 { match (r) { Ok(n) => { return n; }, Err(e) => { return e.len(); } } } function main(): i32 { return f(Ok(3)); }`, 0},
		// A user generic enum's payload is its spelling with the enum's
		// parameters replaced by the scrutinee's arguments.
		{"generic-enum-match-stmt", `enum Box[T, E] { Full(T), Blank(E) } function f(b: Box[i32, string]): i32 { match (b) { Full(n) => { return n; }, Blank(s) => { return s.len(); } } } function main(): i32 { var b: Box[i32, string] = Full(5); return f(b); }`, 0},
		{"json-value-match-stmt", `function f(v: JsonValue): i32 { match (v) { JNull => { return 1; }, JBool(b) => { return 2; }, _ => { return 0; } } } function main(): i32 { return f(JNull); }`, 0},
		{"map-iter-cursor", `import "core/map"; function f(m: Map[string, i32]): i32 { var it: MapIter[string, i32] = m.iter(); var n: i32 = 0; while (it.has_next()) { n = n + it.key().len() + it.value(); it.advance(); } return n; } function main(): i32 { return 0; }`, 0},
		{"option-literal-scrutinee", `function main(): i32 { match (Some(4)) { Some(v) => { return v + 1; }, None => { return 0; } } }`, 0},
		{"option-inferred-local", `function main(): i32 { var o = Some("ab"); match (o) { Some(v) => { return v.len(); }, None => { return 0; } } }`, 0},
		{"dyn-binding", dynPrelude + `function main(): i32 { var d: dyn Greet = Dog {}; return 0; }`, 0},
		{"dyn-method", dynPrelude + `function main(): i32 { var d: dyn Greet = Dog {}; return d.hi(); }`, 4},
		// Inside a loop the hint names the statement in the body, not the
		// loop that encloses it.
		{"dyn-method-in-loop", dynPrelude + `function main(): i32 {
    var d: dyn Greet = Dog {};
    var n: i32 = 0;
    while (n < 3) {
        n = n + d.hi();
    }
    return n;
}`, 8},
		// A variant assigned to a union-typed local widens, as it does in a
		// declaration or a return.
		{"union-variant-reassign", `struct Circle { r: i32 }
struct Square { s: i32 }
type Shape = Circle | Square;
function main(): i32 { var sh: Shape = Circle { r: 1 }; sh = Square { s: 2 }; return 0; }`, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(dir, tc.name+".fern")
			if err := os.WriteFile(path, []byte(tc.source), 0o644); err != nil {
				t.Fatal(err)
			}
			if out, err := exec.Command(native, "-check", path).CombinedOutput(); err != nil {
				t.Fatalf("native checker rejected the corpus: %v\n%s", err, out)
			}
			cmd := runX86_64Bin(runner, driver)
			cmd.Args = append(cmd.Args, "-check", path, stdlibRoot)
			out, err := cmd.CombinedOutput()
			wantCode := 0
			if tc.fallback != 0 {
				wantCode = 1
			}
			if cmd.ProcessState == nil || cmd.ProcessState.ExitCode() != wantCode {
				t.Fatalf("self-host checker: %v, want exit %d\n%s", err, wantCode, out)
			}
			if tc.fallback != 0 {
				want := `error[type]: "main": the self-host checker could not infer the type of the statement at line ` +
					strconv.Itoa(tc.fallback) + " ("
				if !strings.Contains(string(out), want) {
					t.Errorf("diagnostic = %s, want %q", out, want)
				}
			} else if len(out) != 0 {
				t.Errorf("accepted program emitted a diagnostic: %s", out)
			}
		})
	}
}

// The self-host checker accepts the compiler it is part of (#10188). Every
// statement it cannot type is a place an ill-typed program would compile.
func TestSelfHostChecksItsOwnSources(t *testing.T) {
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "fern.fern")
	gcc, runner := x86_64Tooling(t)
	driver := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")
	src, err := filepath.Abs("../../examples/self_host/fern.fern")
	if err != nil {
		t.Fatal(err)
	}
	stdlib, err := filepath.Abs("../../internal/stdlib")
	if err != nil {
		t.Fatal(err)
	}
	cmd := runX86_64Bin(runner, driver)
	cmd.Args = append(cmd.Args, "-check", src, stdlib)
	out, err := cmd.CombinedOutput()
	if err != nil || len(out) != 0 {
		t.Fatalf("fern-selfhost -check of its own sources: %v\n%s", err, out)
	}
}
