//go:build measure

package arm64

import (
	"os"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/constfold"
	"github.com/jakechampion/lang/internal/modload"
	"github.com/jakechampion/lang/internal/monomorph"
)

func TestMeasureSelfHost(t *testing.T) {
	path := os.Getenv("MEASURE_SRC")
	if path == "" {
		path = "../../../examples/self_host/fern.fern"
	}
	prog, _, err := modload.Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if err := constfold.Fold(prog, nil); err != nil {
		t.Fatalf("constfold: %v", err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if err := monomorph.Run(prog, info); err != nil {
		t.Fatalf("monomorph: %v", err)
	}
	asm, err := EmitWithOptions(prog, info, Options{})
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	n := 0
	for _, l := range strings.Split(asm, "\n") {
		if len(l) > 0 && l[0] == '\t' && !strings.HasPrefix(l, "\t.") {
			n++
		}
	}
	t.Logf("INSTRUCTIONS=%d BYTES=%d", n, len(asm))
	if out := os.Getenv("MEASURE_OUT"); out != "" {
		os.WriteFile(out, []byte(asm), 0o644)
	}
}
