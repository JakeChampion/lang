package coreutils

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

// TestSelinuxMergeContext checks chcon's component arithmetic against
// libselinux's rules (context_new, context_*_set, context_str) on inputs
// chcon only reaches on a kernel WITH SELinux, which none of the corpus
// machines has: a context is two to five colons and no whitespace, only the
// range may hold a colon or a space, every component that fails is reported,
// and an empty value is a value.
func TestSelinuxMergeContext(t *testing.T) {
	probe, err := filepath.Abs(filepath.Join("testdata", "selinux_merge", "main.fern"))
	if err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(e2eharness.BuildLangBinForInterp(t), "-interp", probe).CombinedOutput()
	if err != nil {
		t.Fatalf("interp: %v\n%s", err, out)
	}
	want := []string{
		"ok x:r:t:s0",
		"ok u:r:t:s0:c1",
		"ok u:r:y_t:s0:c0.c1023",
		"ok u:r:t:s0",
		"err failed to create security context: 'u:r': Invalid argument",
		"err failed to create security context: 'u:r:t:a:b:c:d': Invalid argument",
		"err failed to create security context: 'u r:r:t': Invalid argument",
		"err failed to set user security context component to 'a:b': Invalid argument",
		"ok u:r:t:s0 c1",
		"err failed to set role security context component to 'a b': Invalid argument",
		"err failed to set type security context component to 'c:d': Invalid argument",
		"ok :r:t:s0",
	}
	got := strings.Split(strings.TrimSuffix(string(out), "\n"), "\n")
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("merge_context:\n got: %q\nwant: %q", got, want)
	}
}
