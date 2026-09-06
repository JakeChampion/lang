package x86_64

import (
	"strings"
	"testing"
)

// A `target_os()` / `target_arch()` the front end did not fold is answered
// by this backend's own target, never by the IR's arm64-linux default: the
// two natives share a pointer width, so the backend has to say which it is.
func TestUnfoldedTargetCallsNameThisTarget(t *testing.T) {
	asm := compile(t, `function main(): i32 {
    print(target_arch());
    print(target_os());
    return 0;
}`)
	for _, want := range []string{`"x86-64"`, `"linux"`} {
		if !strings.Contains(asm, want) {
			t.Errorf("emitted asm has no %s literal", want)
		}
	}
	if strings.Contains(asm, `"arm64"`) {
		t.Errorf("emitted asm carries an \"arm64\" literal: target_arch() lowered as the IR default, not this backend's ISA")
	}
}
