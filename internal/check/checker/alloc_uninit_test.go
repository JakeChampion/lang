package checker

import (
	"github.com/jakechampion/lang/internal/syntax/parser"
	"strings"
	"testing"
)

func TestUninitializedByteAllocationRequiresUnsafe(t *testing.T) {
	for _, tc := range []struct {
		source string
		want   string
	}{
		{`function main(): i32 { return __alloc_u8_uninit(4).len(); }`, "outside an unsafe block"},
		{`function main(): i32 { unsafe { let bytes: u8[] = __alloc_u8_uninit(4); return bytes.len(); } }`, ""},
	} {
		program, err := parser.Parse(tc.source)
		if err != nil {
			t.Fatal(err)
		}
		_, err = Check(program)
		if tc.want == "" {
			if err != nil {
				t.Fatal(err)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("got %v, want %s", err, tc.want)
		}
	}
}
