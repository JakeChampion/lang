package e2eselfhost

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// The self-host used to return -1 for every Darwin poll, even a ready fd.
// Inherited pipes avoid port races and let the test control readiness exactly.
func TestSelfHostArm64DarwinPoll(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("requires the Apple Silicon execution lane")
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driver := buildSelfHostBinArm64Darwin(t, dir, "asm_ir_run.fern", "driver")
	for _, tc := range []struct {
		name    string
		fds     string
		ready   bool
		timeout int
		want    int
		rounds  int
	}{
		{"empty", "[]", false, 0, -1, 1},
		{"negative", "[0 - 1]", false, 0, -1, 1},
		{"invalid", "[123456]", false, 0, -1, 32},
		{"timeout", "[3]", false, 30, -1, 1},
		{"ready", "[0 - 1, 3]", true, 1000, 1, 32},
		{"zero_timeout", "[3]", true, 0, 0, 32},
		{"mixed_invalid", "[123456, 3]", true, 0, 1, 32},
		{"duplicate", "[0 - 1, 3, 3]", true, 1000, 1, 32},
		{"first", "[4, 3]", true, 1000, 0, 32},
		{"infinite", "[3]", true, -1, 0, 32},
		{"delayed_finite", "[3]", true, 1000, 0, 1},
		{"delayed_infinite", "[3]", true, -1, 0, 1},
		{"tcp_listener", "[0 - 1, 3]", true, 1000, 1, 32},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := fmt.Sprintf(`function exercise(): i32 {
    var fds: i32[] = %s;
    var before: i32 = __syscall3(41, 3, 0, 0);
    if (before < 0 || __syscall3(6, before, 0, 0) != 0) { return 2; }
    var i: i32 = 0;
    while (i < %d) {
        if (poll(fds, %d) != %d) { return 1; }
        i = i + 1;
    }
    var after: i32 = __syscall3(41, 3, 0, 0);
    if (after != before || __syscall3(6, after, 0, 0) != 0) { return 3; }
    return 0;
}
function main(): i32 { return exercise(); }
`, tc.fds, tc.rounds, tc.timeout, tc.want)
			compile := exec.Command(driver, "-target", "arm64-darwin")
			compile.Env = append(os.Environ(), "FERN_STRICT_IR=1", "FERN_LEAKCHECK=1")
			compile.Stdin = strings.NewReader(src)
			asm, err := compile.Output()
			if err != nil {
				t.Fatalf("emit poll: %v", err)
			}
			path := filepath.Join(dir, tc.name+".s")
			if err := os.WriteFile(path, asm, 0o600); err != nil {
				t.Fatal(err)
			}
			bin := filepath.Join(dir, tc.name)
			if out, err := exec.Command("clang", "-nostdlib", "-Wl,-e,_main", "-lSystem", path, "-o", bin).CombinedOutput(); err != nil {
				t.Fatalf("link poll: %v\n%s", err, out)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, bin)
			if tc.name == "tcp_listener" {
				listener, err := net.ListenTCP("tcp4", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
				if err != nil {
					t.Fatal(err)
				}
				defer listener.Close()
				file, err := listener.File()
				if err != nil {
					t.Fatal(err)
				}
				defer file.Close()
				conn, err := net.DialTimeout("tcp4", listener.Addr().String(), time.Second)
				if err != nil {
					t.Fatal(err)
				}
				defer conn.Close()
				cmd.ExtraFiles = append(cmd.ExtraFiles, file)
			}
			for len(cmd.ExtraFiles) < 2 {
				r, w, err := os.Pipe()
				if err != nil {
					t.Fatal(err)
				}
				defer r.Close()
				defer w.Close()
				if tc.ready {
					if strings.HasPrefix(tc.name, "delayed_") {
						timer := time.AfterFunc(50*time.Millisecond, func() { _, _ = w.Write([]byte("x")) })
						defer timer.Stop()
					} else if _, err := w.Write([]byte("x")); err != nil {
						t.Fatal(err)
					}
				}
				cmd.ExtraFiles = append(cmd.ExtraFiles, r)
			}
			start := time.Now()
			out, err := cmd.CombinedOutput()
			elapsed := time.Since(start)
			if err != nil {
				t.Fatalf("poll: %v\n%s", err, out)
			}
			if tc.name == "timeout" && elapsed < 30*time.Millisecond {
				t.Errorf("poll returned before its timeout: %v", elapsed)
			}
			allocs, frees, live := leakSummaryOf(t, tc.name, string(out))
			t.Logf("rounds=%d allocs=%d frees=%d live_bytes=%d", tc.rounds, allocs, frees, live)
			if allocs != frees || live != 0 {
				t.Errorf("poll leaked: allocs=%d frees=%d live_bytes=%d", allocs, frees, live)
			}
		})
	}
}
