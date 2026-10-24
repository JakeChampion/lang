package checker

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/syntax/parser"
)

func TestU8HasItsOwnMethodSurface(t *testing.T) {
	src := `function (b: u8) doubled(): i32 { return (b as i32) * 2; }

function main(): i32 {
    let b: u8 = 21;
    return b.doubled();
}`
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, err := Check(prog); err != nil {
		t.Fatalf("check: %v", err)
	}
}

func TestU8DoesNotInheritU32Methods(t *testing.T) {
	src := `function (n: u32) only_on_u32(): i32 { return n as i32; }

function main(): i32 {
    let b: u8 = 7;
    return b.only_on_u32();
}`
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	_, err = Check(prog)
	if err == nil {
		t.Fatal("a u32 method resolved on a u8 receiver; u8 must have its own surface")
	}
	if !strings.Contains(err.Error(), "u8") {
		t.Errorf("error = %v, want it to name the u8 receiver", err)
	}
}

func TestOtherWidthDispatchUnchanged(t *testing.T) {
	for _, tc := range []struct{ name, src string }{
		{"u32", `function (n: u32) twice(): u32 { return n * 2; }
			 function main(): i32 { let n: u32 = 4; return n.twice() as i32; }`},
		{"i32", `function (n: i32) twice(): i32 { return n * 2; }
			 function main(): i32 { let n: i32 = 4; return n.twice(); }`},
		{"i64", `function (n: i64) twice(): i64 { return n * 2; }
			 function main(): i32 { let n: i64 = 4; return n.twice() as i32; }`},
		{"u64", `function (n: u64) twice(): u64 { return n * 2; }
			 function main(): i32 { let n: u64 = 4; return n.twice() as i32; }`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			prog, err := parser.Parse(tc.src)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if _, err := Check(prog); err != nil {
				t.Fatalf("check: %v", err)
			}
		})
	}
}

func TestU8AndU32SameNameCoexist(t *testing.T) {
	src := `function (b: u8) width(): i32 { return 8; }
function (n: u32) width(): i32 { return 32; }

function main(): i32 {
    let b: u8 = 1;
    let n: u32 = 1;
    if (b.width() != 8) { return 1; }
    if (n.width() != 32) { return 2; }
    return 0;
}`
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, err := Check(prog); err != nil {
		t.Fatalf("check: %v", err)
	}
}
