// `sysctl(mib)`: Darwin's kernel variables by MIB, refused everywhere else.
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

// kern.ostype as text, and the load-average record's length.
const sysctlSource = `function main(): i32 {
    match (sysctl([1, 1])) {
        Ok(bs) => {
            let text: u8[] = [];
            let n: i32 = 0;
            while (n < bs.len() && bs[n] != 0 as u8) {
                text = text.append(bs[n]);
                n = n + 1;
            }
            print(string_from_bytes_unchecked(text));
        },
        Err(_) => { return 2; }
    }
    match (sysctl([2, 2])) {
        Ok(bs) => { if (bs.len() != 24) { return 3; } },
        Err(_) => { return 4; }
    }
    match (sysctl([999999, 1])) {
        Ok(_) => { return 5; },
        Err(_) => {}
    }
    return 0;
}
`

// The build is checked on every host; only Apple Silicon runs it.
func TestArm64DarwinSysctl(t *testing.T) {
	bin := buildFernCLI(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "prog.fern")
	if err := os.WriteFile(src, []byte(sysctlSource), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "prog")
	if o, err := exec.Command(bin, "-target", "arm64-darwin", "-o", out, src).CombinedOutput(); err != nil {
		t.Fatalf("native arm64-darwin build failed: %v\n%s", err, o)
	}
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		return
	}
	got, err := exec.Command(out).Output()
	if err != nil {
		t.Fatalf("run: %v (an exit code names the failing step)", err)
	}
	if string(got) != "Darwin\n" {
		t.Errorf("kern.ostype read as %q, want %q", got, "Darwin\n")
	}
}

// Only Darwin grants `sysctl`: Linux removed sysctl(2) and a component has no
// kernel at all.
func TestSysctlRefusedOffDarwin(t *testing.T) {
	prog, err := parser.Parse(`function main(): i32 { match (sysctl([1, 1])) { Ok(_) => { return 0; }, Err(_) => { return 1; } } }`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, err := checker.Check(prog); err != nil {
		t.Fatalf("check: %v", err)
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux", "arm64-android", "wasm32-wasi", "wasm32-wasi-http"} {
		vs := platforms.Enforce(prog, target)
		if len(vs) == 0 || vs[0].Builtin != "sysctl" || vs[0].Capability != "sysctl" {
			t.Errorf("%s: violations %v, want sysctl refused on the sysctl capability", target, vs)
		}
	}
	if vs := platforms.Enforce(prog, "arm64-darwin"); len(vs) != 0 {
		t.Errorf("arm64-darwin refused sysctl: %v", vs)
	}
}
