package checker

import "testing"

func TestOwnedBuiltinArrayUpdateResults(t *testing.T) {
	const prefix = `function take(own value: u8[]): i32 { return value.len(); }
function copied(input: [u8]): u8[] { let result: u8[] = []; for byte in input { result = result.append(byte); } return result; }
`
	for _, expression := range []string{
		`copied([1u8]).with(0, 2u8)`,
		`copied([1u8]).append(2u8)`,
		`copied([1u8]).truncate(0)`,
		`copied([1u8]).with(0, 2u8).append(3u8)`,
		`value.with(0, 2u8)`,
	} {
		wantOK(t, expression, prefix+`function work(value: u8[]): i32 { return take(`+expression+`); }`)
	}
	for _, expression := range []string{`value.with(0, value[0])`, `value.append(value[0])`, `value.truncate(0)`} {
		wantE051(t, expression, `function take(own value: Writer[]): i32 { return value.len(); }
function work(value: Writer[]): i32 { return take(`+expression+`); }`)
	}
	wantE051(t, "declared method", `struct Holder { writer: Writer }
function (value: Holder) with(index: i32, writer: Writer): Writer { return writer; }
function take(own value: Writer): i32 { return 0; }
function work(value: Holder, writer: Writer): i32 { return take(value.with(0, writer)); }`)
}
