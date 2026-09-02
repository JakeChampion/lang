package e2eselfhost

import (
	"os"
	"strings"
	"testing"
)

// `h = bump(h, i)` with an in-place `own` update hands the same box back, so
// the rebind's __field_reclaim_Holder sees old == new. The nested-struct arm
// releases a carried field without the copy-on-write compare (#6605), which
// on an identity reclaim decs the field's only reference. The helper skips
// the field walk when old and new are one box. Pinned under the sanitizer:
// the plain run reads the freed inner array back intact.
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
