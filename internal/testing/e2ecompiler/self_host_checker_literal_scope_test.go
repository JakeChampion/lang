package e2ecompiler

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

var structLiteralScopeCases = []struct {
	name string
	body string
	want string
}{
	{"plain", `let p = P { zz: 1 };`, "E043,E005"},
	{"map-value", `let m = Map { "p": P { zz: 1 } };`, "E043,E005"},
	{"map-siblings", `let m = Map { "p": P { zz: 1 }, "q": P { zz: 2 } };`, "E043,E005,E043,E005"},
	{"interpolation", `let s = f"x={(P { zz: 1 }).x}";`, "E043,E005"},
	{"match-guard", `match (Some(1)) { Some(n) when (P { zz: 1 }).x == 0 => {}, Some(_) => {}, None => {} }`, "E043,E005"},
	{"lambda", `let f = (): i32 => { let p = P { zz: 1 }; return 0; };`, "E043,E005"},
	{"valid-map-and-guard", `let m = Map { "p": P { x: 1 } }; match (Some(1)) { Some(n) when (P { x: 1 }).x == 0 => {}, Some(_) => {}, None => {} }`, ""},
}

func TestSelfHostCheckerStructLiteralScopeSequenceX86_64(t *testing.T) {
	bin, runner, dir := buildCheckerCodesBin(t)
	for _, tc := range structLiteralScopeCases {
		t.Run(tc.name, func(t *testing.T) {
			src := "import \"core/map\";\nstruct P { x: i32 }\nfunction main(): i32 { " + tc.body + " return 0; }"
			var goCodes []string
			for _, d := range goCheckerDiags(t, dir, src) {
				goCodes = append(goCodes, d.code)
			}
			if got := strings.Join(goCodes, ","); got != tc.want {
				t.Fatalf("Go checker diagnostic sequence = %q, want %q", got, tc.want)
			}
			cmd := exec.Command(bin)
			if len(runner) > 0 {
				cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), bin)...)
			}
			cmd.Stdin = strings.NewReader(src)
			var codes []string
			for _, d := range driverDiags(runCheckerDriver(t, cmd, tc.name)) {
				codes = append(codes, d.code)
			}
			if got := strings.Join(codes, ","); got != tc.want {
				t.Errorf("self-host diagnostic sequence = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestSelfHostCheckerStructLiteralScopeWasm(t *testing.T) {
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH")
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/checker_codes_run.fern")
	bin := buildSelfHostBinFor(t, dir, "drivers/checker_codes_run.fern", "literal_checker.wasm", e2eharness.TargetWasm32Wasi)
	for _, tc := range structLiteralScopeCases {
		t.Run(tc.name, func(t *testing.T) {
			src := "import \"core/map\";\nstruct P { x: i32 }\nfunction main(): i32 { " + tc.body + " return 0; }"
			cmd := exec.Command(wasmtime, "run", bin)
			cmd.Stdin = strings.NewReader(src)
			var codes []string
			for _, d := range driverDiags(runCheckerDriver(t, cmd, tc.name)) {
				codes = append(codes, d.code)
			}
			if got := strings.Join(codes, ","); got != tc.want {
				t.Errorf("Wasm diagnostic sequence = %q, want %q", got, tc.want)
			}
		})
	}
}
