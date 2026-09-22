package e2e

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestPollScratchReclaimed(t *testing.T) {
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		t.Run(target, func(t *testing.T) {
			qemu := ""
			if target == "x86-64-linux" {
				qemu = x86QemuOrEmpty(t)
			} else {
				qemu = arm64QemuOrEmpty(t)
			}
			for _, backend := range []string{"flat", "ssa"} {
				t.Run(backend, func(t *testing.T) { checkPollScratch(t, target, qemu, backend) })
			}
		})
	}
}

func TestArm64DarwinNativePollScratch(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("requires the Apple Silicon execution lane")
	}
	checkPollScratch(t, "arm64-darwin", "", "flat")
}

// The poll helpers must reclaim their temporary kernel buffers on every
// return path, including empty/negative sets and failed registrations (#9853).
func checkPollScratch(t *testing.T, target, qemu, backend string) {
	t.Helper()
	fern := buildFernCLI(t)
	for _, tc := range []struct {
		name, fds string
		want      int
		ready     bool
	}{
		{"empty", "[]", -1, false},
		{"negative", "[0 - 1]", -1, false},
		{"invalid", "[123456]", -1, false},
		{"timeout", "[3]", -1, false},
		{"ready", "[0 - 1, 3]", 1, true},
		{"first", "[3, 4]", 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "probe.fern")
			src := fmt.Sprintf(`function exercise(): i32 {
    var fds: i32[] = %s;
    var i: i32 = 0;
    while (i < 32) {
        if (poll(fds, 0) != %d) { return 1; }
        i = i + 1;
    }
    return 0;
}
function main(): i32 {
    var result: i32 = exercise();
    if (__rc_underflow_count() != 0) { return 99; }
    return result;
}
`, tc.fds, tc.want)
			if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
				t.Fatal(err)
			}
			bin := filepath.Join(dir, "probe")
			compile := exec.Command(fern, "-target", target, "-backend", backend, "-o", bin, path)
			compile.Env = append(os.Environ(), "FERN_LEAKCHECK=1")
			if out, err := compile.CombinedOutput(); err != nil {
				t.Fatalf("compile: %v\n%s", err, out)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, bin)
			if qemu != "" {
				cmd = exec.CommandContext(ctx, qemu, bin)
			}
			for range 2 {
				r, w, err := os.Pipe()
				if err != nil {
					t.Fatal(err)
				}
				defer r.Close()
				defer w.Close()
				if tc.ready {
					if _, err := w.Write([]byte("x")); err != nil {
						t.Fatal(err)
					}
				}
				cmd.ExtraFiles = append(cmd.ExtraFiles, r)
			}
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("poll: %v\n%s", err, out)
			}
			allocs, frees, live := parseLeakCheckLine(t, string(out))
			t.Logf("allocs=%d frees=%d live_bytes=%d", allocs, frees, live)
			if allocs != frees || live != 0 {
				t.Errorf("poll scratch leaked: allocs=%d frees=%d live_bytes=%d", allocs, frees, live)
			}
		})
	}
}
