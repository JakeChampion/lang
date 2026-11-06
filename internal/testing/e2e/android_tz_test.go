package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// On Android, tz.local_zone reads the system property persist.sys.timezone
// and not /etc/localtime. A Linux host has no property areas, so the zone of
// an Android binary there is UTC, whatever the zone of the host is. The
// Android lane runs the same program on an emulator with a zone set.
func TestAndroidLocalZoneWithoutProperties(t *testing.T) {
	if _, err := os.Stat("/dev/__properties__"); err == nil {
		t.Skip("/dev/__properties__ exists, so this host has Android properties")
	}
	for _, target := range []string{"x86-64-android", "arm64-android"} {
		t.Run(target, func(t *testing.T) {
			bin := filepath.Join(t.TempDir(), "zone")
			build := exec.Command(e2eharness.SelfHostCLI(t), "-target", target, "-o", bin,
				e2eharness.RepoPath("tests", "android", "tz", "zone.fern"), e2eharness.SelfHostStdlibRoot(t))
			build.Env = e2eharness.ChildEnv()
			if out, err := build.CombinedOutput(); err != nil {
				t.Fatalf("build for %s: %v\n%s", target, err, out)
			}
			cmd := androidRunner(t, target, bin)
			cmd.Env = []string{}
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("run: %v\n%s", err, out)
			}
			if got, want := string(out), "property \noffset 0\n"; got != want {
				t.Fatalf("output = %q, want %q", got, want)
			}
		})
	}
}
