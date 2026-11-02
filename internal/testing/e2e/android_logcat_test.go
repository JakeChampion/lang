package e2e

import (
	"bytes"
	"encoding/binary"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

const logdPath = "/dev/socket/logdw"

// tests/android/report.fern makes a std/log record, then aborts: one report
// at each logcat priority. The Android lane runs the same program on an
// emulator.
func androidReportBin(t *testing.T, target string) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "report")
	cmd := exec.Command(e2eharness.SelfHostCLI(t), "-target", target, "-o", bin,
		e2eharness.RepoPath("tests", "android", "report.fern"), e2eharness.SelfHostStdlibRoot(t))
	cmd.Env = e2eharness.ChildEnv()
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build for %s: %v\n%s", target, err, out)
	}
	return bin
}

// The command that runs an Android binary on this host: natively for
// x86-64-android on an x86-64 Linux host, under qemu for arm64-android.
func androidRunner(t *testing.T, target, bin string) *exec.Cmd {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skipf("Android binaries run on a Linux host only, not %s", runtime.GOOS)
	}
	if target == "x86-64-android" {
		if runtime.GOARCH != "amd64" {
			t.Skip("x86-64-android runs natively on an x86-64 host only")
		}
		return exec.Command(bin)
	}
	qemu := e2eharness.Arm64Runner(t)
	if qemu == "" {
		return exec.Command(bin)
	}
	return exec.Command(qemu, bin)
}

func TestAndroidReportsWithoutLogd(t *testing.T) {
	if _, err := os.Stat(logdPath); err == nil {
		t.Skipf("%s exists, so this host has a logd", logdPath)
	}
	for _, target := range []string{"x86-64-android", "arm64-android"} {
		t.Run(target, func(t *testing.T) {
			var stderr bytes.Buffer
			cmd := androidRunner(t, target, androidReportBin(t, target))
			cmd.Stderr = &stderr
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != 134 {
				t.Errorf("exit = %d, want 134", code)
			}
			for _, want := range []string{"[WARN] about to index past the end", "fern: array index out of range"} {
				if !strings.Contains(stderr.String(), want) {
					t.Errorf("stderr has no %q:\n%s", want, stderr.String())
				}
			}
		})
	}
}

func TestLinuxImageHasNoLogcat(t *testing.T) {
	bin, err := os.ReadFile(androidReportBin(t, e2eharness.TargetX86_64Linux))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(bin, []byte(logdPath)) {
		t.Errorf("an x86-64-linux image names %s; only the Android targets write to logcat", logdPath)
	}
}

type logdRecord struct {
	logID, priority byte
	tid             uint16
	sec             uint32
	tag, msg        string
}

// parseLogdRecord reads the datagram that liblog writes: a packed header of
// the log ID, the thread ID and the real time, then the priority, the tag
// and the message, each string NUL-terminated.
func parseLogdRecord(d []byte) (logdRecord, bool) {
	if len(d) < 12 || d[len(d)-1] != 0 {
		return logdRecord{}, false
	}
	parts := strings.Split(string(d[12:len(d)-1]), "\x00")
	if len(parts) != 2 {
		return logdRecord{}, false
	}
	return logdRecord{
		logID: d[0], tid: binary.LittleEndian.Uint16(d[1:]), sec: binary.LittleEndian.Uint32(d[3:]),
		priority: d[11], tag: parts[0], msg: parts[1],
	}, true
}

// The records reach logd: a std/log record at priority W (5), and the abort
// at priority F (7), both with the tag "fern".
func TestAndroidReportsReachLogd(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skipf("binding %s needs root; the Android lane checks logcat on an emulator", logdPath)
	}
	if _, err := os.Stat(logdPath); err == nil {
		t.Skipf("%s exists, so this host has a logd", logdPath)
	}
	if err := os.MkdirAll(filepath.Dir(logdPath), 0o755); err != nil {
		t.Skipf("cannot make %s: %v", filepath.Dir(logdPath), err)
	}
	for _, target := range []string{"x86-64-android", "arm64-android"} {
		t.Run(target, func(t *testing.T) {
			cmd := androidRunner(t, target, androidReportBin(t, target))
			conn, err := net.ListenUnixgram("unixgram", &net.UnixAddr{Name: logdPath, Net: "unixgram"})
			if err != nil {
				t.Fatal(err)
			}
			defer os.Remove(logdPath)
			defer conn.Close()
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != 134 {
				t.Errorf("exit = %d, want 134", code)
			}
			var got []logdRecord
			buf := make([]byte, 65536)
			for len(got) < 2 {
				_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
				n, err := conn.Read(buf)
				if err != nil {
					break
				}
				r, ok := parseLogdRecord(buf[:n])
				if !ok {
					t.Fatalf("a datagram is not a logd record: %q", buf[:n])
				}
				got = append(got, r)
			}
			want := []struct {
				priority byte
				msg      string
			}{{5, "[WARN] about to index past the end"}, {7, "fern: array index out of range"}}
			if len(got) != len(want) {
				t.Fatalf("logd got %d records, want %d: %+v", len(got), len(want), got)
			}
			for i, w := range want {
				r := got[i]
				if r.logID != 0 || r.priority != w.priority || r.tag != "fern" || r.msg != w.msg || r.tid == 0 || r.sec < 1700000000 {
					t.Errorf("record %d = %+v, want main log, priority %d, tag fern, message %q", i, r, w.priority, w.msg)
				}
			}
		})
	}
}
