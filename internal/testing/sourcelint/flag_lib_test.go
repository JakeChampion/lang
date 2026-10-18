package sourcelint

import (
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/envflag"
)

// scripts/flag-lib's fern_flag reads a boolean FERN_* variable as
// internal/envflag does, for every value either side treats specially.
func TestFlagLibAgreesWithEnvflag(t *testing.T) {
	lib, err := filepath.Abs(filepath.Join("..", "..", "..", "scripts", "flag-lib"))
	if err != nil {
		t.Fatal(err)
	}
	run := func(env []string, args ...string) bool {
		t.Helper()
		cmd := exec.Command("bash", append([]string{"-c", `. "$0"; fern_flag "$@"`, lib}, args...)...)
		cmd.Env = ciEnv(env...)
		out, err := cmd.CombinedOutput()
		if err == nil {
			return true
		}
		if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() == 1 && len(out) == 0 {
			return false
		}
		t.Fatalf("fern_flag %v: %v\n%s", args, err, out)
		return false
	}
	for _, v := range []string{"", "0", "false", "FALSE", "Off", "off", "1", "true", "yes", "quiet", "00", " 0", "no"} {
		if got, want := run([]string{"FERN_FLAG_LIB_TEST=" + v}, "FERN_FLAG_LIB_TEST"), envflag.ValueOn(v); got != want {
			t.Errorf("FERN_FLAG_LIB_TEST=%q: fern_flag = %v, envflag.ValueOn = %v", v, got, want)
		}
	}
	if run(nil, "FERN_FLAG_LIB_TEST") {
		t.Error("an unset variable reads as on")
	}
	if !run(nil, "FERN_FLAG_LIB_TEST", "1") {
		t.Error("an unset variable with UNSET=1 reads as off")
	}
	if run([]string{"FERN_FLAG_LIB_TEST=0"}, "FERN_FLAG_LIB_TEST", "1") {
		t.Error("UNSET=1 turned on a variable set to 0")
	}
}
