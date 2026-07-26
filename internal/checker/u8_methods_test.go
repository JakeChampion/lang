package checker

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/parser"
)

// `u8` had no method surface of its own: methodTypeName mapped every
// unsigned non-64-bit width to "u32", so a byte silently reached u32's
// methods and nothing could declare a method ON a byte. That blocks moving
// the byte classifiers to `u8` with `ascii` in their names (#5629 slice 3),
// which is what makes the ASCII/Unicode split a type distinction rather than
// a naming convention.
//
// Nothing in the tree declared a u8 receiver, so giving u8 its own name is
// additive for declarations and strict for dispatch: a byte no longer
// resolves u32's methods, exactly as `char` does not resolve i32's.
func TestU8HasItsOwnMethodSurface(t *testing.T) {
	src := `function (b: u8) doubled(): i32 { return (b as i32) * 2; }

function main(): i32 {
    var b: u8 = 21;
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

// The strictness half: a method declared on `u32` is NOT reachable from a
// `u8` receiver. Before this change it was, because both mapped to "u32".
func TestU8DoesNotInheritU32Methods(t *testing.T) {
	src := `function (n: u32) only_on_u32(): i32 { return n as i32; }

function main(): i32 {
    var b: u8 = 7;
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
	// The rejection is the contract; the wording is the pre-existing E043
	// scalar-method-not-found phrasing that #5494 tracks separately (it
	// talks about struct field access for what the user wrote as a method
	// call). Assert the receiver type is named, not the exact sentence.
	if !strings.Contains(err.Error(), "u8") {
		t.Errorf("error = %v, want it to name the u8 receiver", err)
	}
}

// The other widths keep their existing surfaces — this change must not
// disturb u32 / i32 / i64 / u64 dispatch.
func TestOtherWidthDispatchUnchanged(t *testing.T) {
	for _, tc := range []struct{ name, src string }{
		{"u32", `function (n: u32) twice(): u32 { return n * 2; }
			 function main(): i32 { var n: u32 = 4; return n.twice() as i32; }`},
		{"i32", `function (n: i32) twice(): i32 { return n * 2; }
			 function main(): i32 { var n: i32 = 4; return n.twice(); }`},
		{"i64", `function (n: i64) twice(): i64 { return n * 2; }
			 function main(): i32 { var n: i64 = 4; return n.twice() as i32; }`},
		{"u64", `function (n: u64) twice(): u64 { return n * 2; }
			 function main(): i32 { var n: u64 = 4; return n.twice() as i32; }`},
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

// A u8 method and a u32 method of the SAME name coexist and each dispatches
// to its own receiver width — the property that lets slice 3 give the byte
// classifiers ascii-named u8 forms without disturbing u32.
func TestU8AndU32SameNameCoexist(t *testing.T) {
	src := `function (b: u8) width(): i32 { return 8; }
function (n: u32) width(): i32 { return 32; }

function main(): i32 {
    var b: u8 = 1;
    var n: u32 = 1;
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
