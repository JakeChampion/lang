package interp

import (
	"testing"
)

func TestTempDirRejectsMalformedHostRoot(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir()+"/bad_\xff")
	value, err := builtinTempDir(nil, []Value{String("prefix")})
	if err != nil {
		t.Fatal(err)
	}
	res, ok := value.(*Enum)
	if !ok || res.VariantName != "Err" {
		t.Fatalf("got %#v, want Err", value)
	}
	ioe, ok := res.Payloads[0].(*Enum)
	if !ok || ioe.VariantName != "InvalidUtf8" || ioe.Index != 3 || len(ioe.Payloads) != 1 || ioe.Payloads[0] != String("prefix") {
		t.Fatalf("got %#v, want InvalidUtf8(prefix)", res.Payloads[0])
	}
}
