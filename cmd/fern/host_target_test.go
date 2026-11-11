package main

import (
	"runtime"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/pkg/platforms"
)

func TestHostTargetIsATargetOfThisMachine(t *testing.T) {
	got := hostTarget()
	if platforms.ForTarget(got) == nil {
		t.Fatalf("hostTarget() = %q, which is no target", got)
	}
	isa := map[string]string{"arm64": "arm64", "amd64": "x86-64"}[runtime.GOARCH]
	if isa == "" || platforms.ForTarget(isa+"-"+runtime.GOOS) == nil {
		if got != "arm64-linux" {
			t.Errorf("hostTarget() = %q on a host that is no target, want arm64-linux", got)
		}
		return
	}
	if !strings.HasPrefix(got, isa+"-") || !strings.HasSuffix(got, "-"+runtime.GOOS) {
		t.Errorf("hostTarget() = %q on %s/%s", got, runtime.GOOS, runtime.GOARCH)
	}
}
