package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostMatchPayloadCaptureIRX86_64 pins the Option/Result match-arm
// payload binding capture resolution (cap_type_in_stmts' StmtMatch arm): a
// lambda capturing a `Some(v)` / `Ok(v)` / `Err(v)` payload binding used to
// resolve cap_type "" (the binding is not a `var`), so the closure lift
// declined and the whole module fell to the AST path — where a capturing
// lambda in a struct fn FIELD miscompiles (silent wrong values: native 20
// read back as 4). The binding now resolves from the scrutinee's Option /
// Result type spelling (opt_payload_type), so these shapes lower via the IR
// path (asserted via the .Lir_ label witness) and compute the native values,
// including a `string` payload whose captured var must dispatch `.len()`
// correctly. (The USER-ENUM variant-payload case needs the enum decls threaded
// into the lift pass — tracked as #5155.)
func TestSelfHostMatchPayloadCaptureIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile("../../examples/self_host/asm_run.fern")
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")

	cases := []struct {
		name string
		src  string
		want int
	}{
		{"option-i32-payload",
			`struct H { f: (i32) => i32, id: i32 } function g(o: Option[i32]): i32 { var r: i32 = 0; match (o) { Some(v) => { var h: H = H { f: function (x: i32): i32 { return x + v; }, id: v }; r = h.f(10) + h.id; }, None => { r = 0; } } return r; } function main(): i32 { return g(Some(5)); }`,
			20},
		{"result-ok-payload",
			`struct H { f: (i32) => i32, id: i32 } function g(r: Result[i32, i32]): i32 { var acc: i32 = 0; match (r) { Ok(v) => { var h: H = H { f: function (x: i32): i32 { return x + v; }, id: v }; acc = h.f(10) + h.id; }, Err(e) => { acc = e; } } return acc; } function main(): i32 { return g(Ok(6)); }`,
			22},
		{"option-string-payload",
			`struct H { f: (i32) => i32, id: i32 } function g(o: Option[string]): i32 { var r: i32 = 0; match (o) { Some(s) => { var h: H = H { f: function (x: i32): i32 { return x + s.len(); }, id: 2 }; r = h.f(10) + h.id; }, None => { r = 0; } } return r; } function main(): i32 { return g(Some("abc")); }`,
			15},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src))
			if len(asm) == 0 {
				t.Fatalf("%s: self-host compiler emitted 0 bytes", tc.name)
			}
			if !strings.Contains(string(asm), ".Lir_") {
				t.Fatalf("%s: emitted asm has no IR-path labels — the payload capture fell back to the AST path", tc.name)
			}
			bin := buildBin(t, gcc, dir, tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(bin)
			} else {
				cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), bin)...)
			}
			_ = cmd.Run()
			if got := cmd.ProcessState.ExitCode(); got != tc.want {
				t.Errorf("%s exited %d, want %d", tc.name, got, tc.want)
			}
		})
	}
}
