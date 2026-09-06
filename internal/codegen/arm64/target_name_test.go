package arm64

import (
	"strings"
	"testing"
)

// A `target_os()` / `target_arch()` the front end did not fold is answered
// by this backend's own target — and Options.Darwin is part of that target.
func TestUnfoldedTargetCallsNameThisTarget(t *testing.T) {
	src := `function main(): i32 {
    print(target_arch());
    print(target_os());
    return 0;
}`
	for _, tc := range []struct {
		opts       Options
		wantOS     string
		notWantOS  string
		wantArch   string
		notWantArc string
	}{
		{Options{}, `"linux"`, `"darwin"`, `"arm64"`, `"x86-64"`},
		{Options{Darwin: true}, `"darwin"`, `"linux"`, `"arm64"`, `"x86-64"`},
	} {
		asm := compile(t, src, tc.opts)
		for _, want := range []string{tc.wantOS, tc.wantArch} {
			if !strings.Contains(asm, want) {
				t.Errorf("darwin=%v: emitted asm has no %s literal", tc.opts.Darwin, want)
			}
		}
		for _, notWant := range []string{tc.notWantOS, tc.notWantArc} {
			if strings.Contains(asm, notWant) {
				t.Errorf("darwin=%v: emitted asm carries a %s literal", tc.opts.Darwin, notWant)
			}
		}
	}
}
