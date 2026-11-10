package printer

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/syntax/parser"
)

func TestValueAttributeRoundtrip(t *testing.T) {
	in := "@value pub opaque struct Key { bytes: u8[] }\n"
	got := formatSrc(t, in)
	if !strings.HasPrefix(got, "@value\npub opaque struct Key") {
		t.Fatalf("value attribute lost: %s", got)
	}
	p, err := parser.Parse(got)
	if err != nil || !p.Structs[0].ValueType {
		t.Fatalf("value attribute lost after reparse: %v", err)
	}
	if Format(p) != got || !strings.HasPrefix(Print(p), "@value\n") {
		t.Fatal("the formatter or printer lost the value attribute")
	}
}
