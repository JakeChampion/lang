package checker

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOwnedFieldHolderShadowsItsGlobalCallee(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("..", "..", "..", "conformance", "cases", "field_holder_shadows_callee", "main.fern"))
	if err != nil {
		t.Fatal(err)
	}
	wantOK(t, "global callee and local holder", string(source))
}
