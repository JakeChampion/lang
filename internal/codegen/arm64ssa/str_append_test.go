package arm64ssa

import (
	"fmt"
	"strings"
	"testing"
)

// __fern_str_append is __str_concat here: this heap never frees a string, so
// the consumed accumulator is left behind either way. The body is a branch,
// and the dependency edge is what puts __str_concat in the module.
func TestStrAppendIsAConcat(t *testing.T) {
	var b strings.Builder
	runtimeHelperEmitters["__fern_str_append"](func(format string, args ...any) {
		b.WriteString(strings.TrimSpace(fmt.Sprintf(format, args...)) + "\n")
	})
	if want := "b " + fnLabel("__str_concat") + "\n"; !strings.HasSuffix(b.String(), want) {
		t.Errorf("__fern_str_append does not end in %q:\n%s", strings.TrimSpace(want), b.String())
	}
	deps := runtimeHelperDeps["__fern_str_append"]
	if len(deps) != 1 || deps[0] != "__str_concat" {
		t.Errorf("__fern_str_append depends on %v, want [__str_concat]", deps)
	}
}
