package e2ecompiler

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func ownedVariantCases(t *testing.T, bin string, runner []string, dir string) {
	t.Helper()
	const decls = `struct H { fd: i32 }
function (own h: H) close(): i32 { return h.fd; }
struct Q { h: Option[H], n: i32 }
function mk(): own H { return H { fd: 1 }; }
function takeo(own o: Option[H]): i32 { return 1; }
function takeoo(own o: Option[Option[H]]): i32 { return 1; }
function takeq(own q: Q): i32 { return q.n; }
function taken(own o: Option[i32]): i32 { return 1; }
`
	cases := []struct{ name, src, want string }{
		{"borrowed-payload", `function f(w: H): i32 { return takeo(Some(w)); }`, "E051"},
		{"borrowed-payload-nested", `function f(w: H): i32 { return takeoo(Some(Some(w))); }`, "E051"},
		{"borrowed-payload-in-field", `function f(w: H): i32 { return takeq(Q { h: Some(w), n: 1 }); }`, "E051"},
		{"borrowed-payload-in-local", `function f(w: H): i32 { let o = Some(w); return takeo(o); }`, "E051"},
		{"borrowed-payload-in-literal-local", `function f(w: H): i32 { let q = Q { h: Some(w), n: 1 }; return takeq(q); }`, "E051"},
		{"owned-payload-read-again", `function f(own w: H): i32 { let o = Some(w); let n = takeo(o); return n + w.close(); }`, "E051"},
		{"owned-payload", `function f(own w: H): i32 { return takeo(Some(w)); }`, ""},
		{"owned-payload-nested", `function f(own w: H): i32 { return takeoo(Some(Some(w))); }`, ""},
		{"fresh-payload", `function f(): i32 { return takeo(Some(mk())); }`, ""},
		{"no-payload", `function f(): i32 { return takeo(None); }`, ""},
		{"payload-holds-no-handle", `function f(n: i32): i32 { return taken(Some(n)); }`, ""},
		{"owned-payload-in-local", `function f(own w: H): i32 { let o = Some(w); return takeo(o); }`, ""},
		{"owned-payload-in-literal-local", `function f(own w: H): i32 { let q = Q { h: Some(w), n: 1 }; return takeq(q); }`, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := decls + tc.src + ` function main(): i32 { return 0; }`
			check := func(label string, ds []driverDiag) {
				t.Helper()
				if tc.want == "" && len(ds) == 0 {
					return
				}
				for _, d := range ds {
					if tc.want != "" && d.code == tc.want {
						return
					}
				}
				t.Fatalf("%s: want %q, got %v", label, tc.want, ds)
			}
			check("Go", goCheckerDiags(t, dir, src))
			cmd := exec.Command(bin)
			if len(runner) > 0 {
				cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), bin)...)
			}
			cmd.Stdin = strings.NewReader(src)
			check("self-host", driverDiags(runCheckerDriver(t, cmd, tc.name)))
		})
	}
}

func TestSelfHostOwnedVariantX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/checker_codes_run.fern")
	bin := buildSelfHostBin(t, gcc, dir, "drivers/checker_codes_run.fern", "owned_variant_checker")
	ownedVariantCases(t, bin, runner, dir)
}

func TestSelfHostOwnedVariantWasm(t *testing.T) {
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH")
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/checker_codes_run.fern")
	bin := buildSelfHostBinFor(t, dir, "drivers/checker_codes_run.fern", "owned_variant_checker.wasm", e2eharness.TargetWasm32Wasi)
	ownedVariantCases(t, bin, []string{wasmtime, "run"}, dir)
}
