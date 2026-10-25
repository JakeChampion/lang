package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/jakechampion/lang/internal/check/checker"
	"github.com/jakechampion/lang/internal/pkg/platforms"
	"github.com/jakechampion/lang/internal/syntax/parser"
)

func TestRingAndKTLSRefusedOffLinux(t *testing.T) {
	for _, c := range []struct{ builtin, capability, src string }{
		{"ring_new", "uring", `function main(): i32 { if (ring_new(8) < 0 as i64) { return 1; } return 0; }`},
		{"tcp_ktls", "ktls", `function main(): i32 { let b: u8[] = []; return tcp_ktls(3, 1, 0, b); }`},
	} {
		prog, err := parser.Parse(c.src)
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		if _, err := checker.Check(prog); err != nil {
			t.Fatalf("check: %v", err)
		}
		for _, target := range []string{"arm64-darwin", "wasm32-wasi", "wasm32-wasi-http"} {
			vs := platforms.Enforce(prog, target)
			if len(vs) == 0 || vs[0].Builtin != c.builtin || vs[0].Capability != c.capability {
				t.Errorf("%s: violations %v, want %s refused on the %s capability", target, vs, c.builtin, c.capability)
			}
		}
		for _, target := range []string{"x86-64-linux", "arm64-linux", "arm64-android"} {
			if vs := platforms.Enforce(prog, target); len(vs) != 0 {
				t.Errorf("%s refused %s: %v", target, c.builtin, vs)
			}
		}
	}
}

func TestPollablesRefusedOffWasi(t *testing.T) {
	for _, c := range []struct{ builtin, capability, src string }{
		{"wasm_timer_pollable", "pollable", `function main(): i32 { return wasm_timer_pollable(5i64); }`},
		{"wasm_pollable_drop", "pollable", `function main(): i32 { return wasm_pollable_drop(3); }`},
		{"wasm_block", "pollable", `function main(): i32 { return wasm_block(3); }`},
		{"wasm_poll", "pollable", `function main(): i32 { let p: i32[] = [3]; return wasm_poll(p); }`},
	} {
		prog, err := parser.Parse(c.src)
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		if _, err := checker.Check(prog); err != nil {
			t.Fatalf("check: %v", err)
		}
		for _, target := range []string{"x86-64-linux", "arm64-linux", "arm64-android", "arm64-darwin", "arm64-freestanding", "x86-64-freestanding"} {
			vs := platforms.Enforce(prog, target)
			if len(vs) == 0 || vs[0].Builtin != c.builtin || vs[0].Capability != c.capability {
				t.Errorf("%s: violations %v, want %s refused on the %s capability", target, vs, c.builtin, c.capability)
			}
		}
		for _, target := range []string{"wasm32-wasi", "wasm32-wasi-http"} {
			if vs := platforms.Enforce(prog, target); len(vs) != 0 {
				t.Errorf("%s refused %s: %v", target, c.builtin, vs)
			}
		}
	}
}

func TestPollableWrappersBuildEverywhere(t *testing.T) {
	bin := buildFernCLI(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "prog.fern")
	prog := `import "std/async";
function main(): i32 {
    let d: async.RealDriver = async.real_driver();
    let timer: i32 = d.timer(1000000i64);
    let dropped: i32 = d.drop_token(timer);
    let ready: i32 = async.wait_any([0, 1], 0);
    if (target_os() == "wasi") {
        return 0;
    }
    if (timer != -1 || dropped != 0) {
        return 1;
    }
    return 0;
}
`
	if err := os.WriteFile(src, []byte(prog), 0o644); err != nil {
		t.Fatal(err)
	}
	host := map[string]string{"linux/amd64": "x86-64-linux", "linux/arm64": "arm64-linux", "darwin/arm64": "arm64-darwin"}[runtime.GOOS+"/"+runtime.GOARCH]
	for _, target := range []string{"x86-64-linux", "arm64-linux", "arm64-android", "arm64-darwin", "wasm32-wasi"} {
		out := filepath.Join(dir, "prog-"+target)
		if o, err := exec.Command(bin, "-target", target, "-o", out, src).CombinedOutput(); err != nil {
			t.Errorf("%s: build failed: %v\n%s", target, err, o)
			continue
		}
		if target == host {
			if o, err := exec.Command(out).CombinedOutput(); err != nil {
				t.Errorf("%s: a native driver's timer is not -1 or its drop not 0: %v\n%s", target, err, o)
			}
		}
	}
}

func TestLinuxOnlyWrappersBuildEverywhere(t *testing.T) {
	bin := buildFernCLI(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "prog.fern")
	prog := `import "std/async";
import "std/tls/ktls";
function main(): i32 {
    let d: async.RealDriver = async.real_driver_io_uring();
    match (ktls.read_now(3)) {
        ktls.ReadFailed(_) => {},
        _ => { return 2; }
    }
    if (d.io_uring()) {
        return 1;
    }
    return 0;
}
`
	if err := os.WriteFile(src, []byte(prog), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux", "arm64-android", "arm64-darwin", "wasm32-wasi"} {
		out := filepath.Join(dir, "prog-"+target)
		if o, err := exec.Command(bin, "-target", target, "-o", out, src).CombinedOutput(); err != nil {
			t.Errorf("%s: build failed: %v\n%s", target, err, o)
		}
	}
}
