package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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
	const dynPrelude = `trait Greet { function hi(self: Self): i32; }
struct Dog {}
impl Greet for Dog { function hi(self: Self): i32 { return 7; } }
`
	const hint = "the self-host checker could not infer an expression's type " +
		"(for example, a dyn method call or an unregistered builtin); " +
		"run native `fern -check` for further diagnostics - see issue #4451"
	for _, tc := range []struct {
		name     string
		source   string
		fallback bool
	}{
		{"enum", `enum Color { Red, Blue } function main(): i32 { var c: Color = Red; return match (c) { Red => 0, Blue => 1 }; }`, false},
		{"generic", `function ident[T](v: T): T { return v; } function main(): i32 { return ident(0); }`, false},
		{"option", `function main(): i32 { var o: Option[i32] = Some(3); return match (o) { Some(n) => n, None => 0 }; }`, false},
		{"result", `function main(): i32 { var r: Result[i32, string] = Ok(3); return match (r) { Ok(n) => n, Err(_) => 0 }; }`, false},
		// A match STATEMENT on a built-in enum: Option's and Result's variants
		// have no struct sig, and JsonValue is in no union table.
		{"option-match-stmt", `function f(o: Option[i32]): i32 { match (o) { Some(n) => { return n; }, None => { return 0; } } } function main(): i32 { return f(Some(3)); }`, false},
		{"result-match-stmt", `function f(r: Result[i32, string]): i32 { match (r) { Ok(n) => { return n; }, Err(e) => { return e.len(); } } } function main(): i32 { return f(Ok(3)); }`, false},
		{"json-value-match-stmt", `function f(v: JsonValue): i32 { match (v) { JNull => { return 1; }, JBool(b) => { return 2; }, _ => { return 0; } } } function main(): i32 { return f(JNull); }`, false},
		{"map-iter-cursor", `import "core/map"; function f(m: Map[string, i32]): i32 { var it: MapIter[string, i32] = m.iter(); var n: i32 = 0; while (it.has_next()) { n = n + it.key().len() + it.value(); it.advance(); } return n; } function main(): i32 { return 0; }`, false},
		{"option-literal-scrutinee", `function main(): i32 { match (Some(4)) { Some(v) => { return v + 1; }, None => { return 0; } } }`, false},
		{"option-inferred-local", `function main(): i32 { var o = Some("ab"); match (o) { Some(v) => { return v.len(); }, None => { return 0; } } }`, false},
		{"dyn-binding", dynPrelude + `function main(): i32 { var d: dyn Greet = Dog {}; return 0; }`, false},
		{"dyn-method", dynPrelude + `function main(): i32 { var d: dyn Greet = Dog {}; return d.hi(); }`, true},
		{"unregistered-builtin", `function main(): i32 { var n: i32 = __rc_underflow_count(); return n; }`, true},
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
			cmd.Args = append(cmd.Args, "-check", path)
			out, err := cmd.CombinedOutput()
			wantCode := 0
			if tc.fallback {
				wantCode = 1
			}
			if cmd.ProcessState == nil || cmd.ProcessState.ExitCode() != wantCode {
				t.Fatalf("self-host checker: %v, want exit %d\n%s", err, wantCode, out)
			}
			if tc.fallback {
				want := `error[type]: "main": ` + hint
				if !strings.Contains(string(out), want) {
					t.Errorf("diagnostic = %s, want %q", out, want)
				}
			} else if len(out) != 0 {
				t.Errorf("accepted program emitted a diagnostic: %s", out)
			}
		})
	}
}
