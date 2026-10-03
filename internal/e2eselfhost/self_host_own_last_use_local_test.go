package e2eselfhost

import (
	"os"
	"path/filepath"
	"testing"
)

// TestSelfHostOwnLastUseLocal runs the conformance case for a local handed to
// an `own` parameter where it dies (#9541) through the self-host CLI with the
// leak census on. The fixture legs check its exit code; this checks that the
// semantic lowering moves each local exactly once — a reference released
// twice or never shows here and nowhere else on the self-host path.
func TestSelfHostOwnLastUseLocal(t *testing.T) {
	prog, err := os.ReadFile(filepath.Join(langSrcAbs(t, "conformance"), "cases", "own_param_last_use_local", "main.fern"))
	if err != nil {
		t.Fatal(err)
	}
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			stderr, code := cli.exitOf(t, string(prog), target, "FERN_LEAKCHECK=1")
			if code != 33 {
				t.Fatalf("exit %d, want 33 (99 is an rc underflow)\n%s", code, stderr)
			}
			assertBalancedCensus(t, stderr)
		})
	}
}

// ownLastUseReassignSrc stores a borrowed parameter over a fresh `let` local
// before handing it on, once straight and once on one branch: the local no
// longer holds a count of its own, so the hand-over has to buy one.
const ownLastUseReassignSrc = `struct W { d: i32[], n: i32 }
@noinline function eat(own w: W): i32 { return w.n + w.d.len(); }
@noinline function mkw(n: i32): W { return W { d: [n], n: n }; }
@noinline function reassign_param(w0: W): i32 { let w: W = mkw(7); w = w0; let r: i32 = eat(w); return r; }
@noinline function branch_param(w0: W, c: i32): i32 { let w: W = mkw(7); if (c > 1) { w = w0; } else { w = mkw(c); } let r: i32 = eat(w); return r; }
function main(): i32 {
    let total: i32 = 0;
    let i: i32 = 0;
    while (i < 3) { let w: W = mkw(i); total = total + reassign_param(w) + branch_param(w, i) + w.n; i = i + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return total % 100;
}
`

// TestSelfHostOwnLastUseLocalSanitized runs the same hand-overs, and a local
// reassigned before its hand-over, under the sanitizer and the leak census.
func TestSelfHostOwnLastUseLocalSanitized(t *testing.T) {
	prog, err := os.ReadFile(filepath.Join(langSrcAbs(t, "conformance"), "cases", "own_param_last_use_local", "main.fern"))
	if err != nil {
		t.Fatal(err)
	}
	cli := buildSelfHostCLI(t)
	for _, c := range []struct {
		name, src string
		want      int
	}{
		{"conformance", string(prog), 33},
		{"reassigned", ownLastUseReassignSrc, 15},
	} {
		t.Run(c.name, func(t *testing.T) {
			stderr, code := cli.exitOf(t, c.src, "x86-64-linux", "FERN_SANITIZE=1")
			if code != c.want || forArrStructSanitizerFault(stderr, true) {
				t.Fatalf("exit %d, want %d (99 is an rc underflow), and no sanitizer report\n%s", code, c.want, stderr)
			}
			stderr, code = cli.exitOf(t, c.src, "x86-64-linux", "FERN_LEAKCHECK=1")
			if code != c.want {
				t.Fatalf("leakcheck: exit %d, want %d\n%s", code, c.want, stderr)
			}
			assertBalancedCensus(t, stderr)
		})
	}
}
