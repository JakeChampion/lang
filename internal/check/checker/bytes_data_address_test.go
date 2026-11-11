package checker

import (
	"strings"
	"testing"
)

// The address from __bytes_data is valid for the call that takes it as an
// argument. Each other place keeps the address after that call.
func TestBytesDataAddressIsADirectArgument(t *testing.T) {
	kept := map[string]string{
		"local":        `function main(): i32 { let b: u8[] = [7u8]; let v: i32 = 0; unsafe { let p: usize = __bytes_data(b); v = __load_u8(p); } return v - 7; }`,
		"offset":       `function main(): i32 { let b: u8[] = [7u8]; let v: i32 = 0; unsafe { v = __load_u8(__bytes_data(b) + 0 as usize); } return v - 7; }`,
		"result":       `function addr(b: u8[]): usize { unsafe { return __bytes_data(b); } } function main(): i32 { let b: u8[] = [7u8]; return addr(b) as i32 * 0; }`,
		"struct field": `struct Box { p: usize } function main(): i32 { let b: u8[] = [7u8]; let n = 0; unsafe { let x = Box { p: __bytes_data(b) }; n = __load_u8(x.p); } return n - 7; }`,
	}
	for name, src := range kept {
		err := checkSource(t, src)
		if err == nil || !hasCode(err, "E095") || !strings.Contains(err.Error(), "is kept after its call") {
			t.Errorf("%s: want E095 for a kept address, got %v", name, err)
		}
	}
	for name, src := range map[string]string{
		"argument":               `function main(): i32 { let b: u8[] = [7u8]; let v: i32 = 0; unsafe { v = __load_u8(__bytes_data(b)); } return v - 7; }`,
		"argument of a slice":    `function main(): i32 { let b: u8[] = [1u8, 7u8]; let v: i32 = 0; unsafe { v = __load_u8(__bytes_data(b[1:2])); } return v - 7; }`,
		"argument of a function": `function sum(p: usize, n: i32): i32 { return n; } function main(): i32 { let b: u8[] = [7u8]; unsafe { return sum(__bytes_data(b), b.len()) - 1; } }`,
	} {
		wantOK(t, name, src)
	}
}

// Outside an unsafe block the call has one diagnostic, not two.
func TestBytesDataOutsideABlockHasOneDiagnostic(t *testing.T) {
	err := checkSource(t, `function main(): i32 { let b: u8[] = [1u8]; let p: usize = __bytes_data(b); return 0; }`)
	if err == nil || !hasCode(err, "E095") || strings.Count(err.Error(), "outside an unsafe block") != 1 || strings.Contains(err.Error(), "is kept after its call") {
		t.Fatalf("want one E095 for the call outside a block, got %v", err)
	}
}
