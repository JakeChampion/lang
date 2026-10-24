package e2ecompiler

import (
	"os"
	"strings"
	"testing"
)

func TestSelfHostIdentityReclaimKeepsNestedFieldX86_64(t *testing.T) {
	src, err := os.ReadFile(langSrcAbs(t, "conformance/cases/struct_nested_field_identity_rebind/main.fern"))
	if err != nil {
		t.Fatal(err)
	}
	bin, runner := sanSelfHostBuild(t, "ident_reclaim", string(src), []string{"FERN_SANITIZE=1"})
	stderr, code := hevRun(t, runner, bin)
	if code != 18 {
		t.Errorf("exit=%d, want 18 (the inner array read back after every rebind)", code)
	}
	if strings.Contains(stderr, "fern-sanitizer: use-after-free") {
		t.Errorf("the identity reclaim released the nested field: %q", stderr)
	}
}
