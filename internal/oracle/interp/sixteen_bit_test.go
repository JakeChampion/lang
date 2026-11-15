package interp

import "testing"

func TestSixteenBitArithmeticWraps(t *testing.T) {
	for _, tc := range []struct {
		name, source string
		want         Number
	}{
		{"u16 add", `function main(): u16 { let x: u16 = 65535; return x + 1; }`, 0},
		{"u16 sub", `function main(): u16 { let x: u16 = 0; return x - 1; }`, 65535},
		{"u16 mul", `function main(): u16 { let x: u16 = 300; return x * 300; }`, 24464},
		{"u16 shl", `function main(): u16 { let x: u16 = 0x8001; return x << 1; }`, 2},
		{"u16 shr is logical", `function main(): u16 { let x: u16 = 0x8000; return x >> 15; }`, 1},
		{"i16 add", `function main(): i16 { let x: i16 = 32767; return x + 1; }`, -32768},
		{"i16 sub", `function main(): i16 { let x: i16 = -32768; return x - 1; }`, 32767},
		{"i16 shl", `function main(): i16 { let x: i16 = 0x4000; return x << 1; }`, -32768},
		{"i16 shr is arithmetic", `function main(): i16 { let x: i16 = -32768; return x >> 15; }`, -1},
		{"i16 least over minus one", `function main(): i16 { let x: i16 = -32768; let y: i16 = -1; return x / y; }`, -32768},
		{"i16 least rem minus one", `function main(): i16 { let x: i16 = -32768; let y: i16 = -1; return x % y; }`, 0},
		{"u16 saturating add", `function main(): u16 { let x: u16 = 65000; return x +| 1000; }`, 65535},
		{"u16 saturating sub", `function main(): u16 { let x: u16 = 5; return x -| 6; }`, 0},
		{"i16 saturating add", `function main(): i16 { let x: i16 = 32000; return x +| 1000; }`, 32767},
		{"i16 saturating sub", `function main(): i16 { let x: i16 = -32000; return x -| 1000; }`, -32768},
		{"i16 saturating mul", `function main(): i16 { let x: i16 = -200; return x *| 200; }`, -32768},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := evalProgramValue(t, tc.source)
			if n, ok := got.(Number); !ok || n != tc.want {
				t.Fatalf("got %v, want %v\n%s", got, tc.want, tc.source)
			}
		})
	}
}

func TestSixteenBitConversions(t *testing.T) {
	for _, tc := range []struct {
		name, source string
		want         Number
	}{
		{"i32 narrows to u16", `function main(): u16 { let x = 70000; return x as u16; }`, 4464},
		{"i32 narrows to i16", `function main(): i16 { let x = 40000; return x as i16; }`, -25536},
		{"i16 reads as u16", `function main(): u16 { let x: i16 = -1; return x as u16; }`, 65535},
		{"u16 reads as i16", `function main(): i16 { let x: u16 = 65535; return x as i16; }`, -1},
		{"i64 narrows to u16", `function main(): u16 { let x: i64 = 4294901761; return x as u16; }`, 1},
		{"f64 narrows to u16", `function main(): u16 { let x: f64 = 70000.7; return x as u16; }`, 4464},
		{"f64 narrows to i16", `function main(): i16 { let x: f64 = -3.9; return x as i16; }`, -3},
		{"u16 widens to u32", `function main(): u32 { let x: u16 = 65535; let y: u32 = x; return y + 1; }`, 65536},
		{"i16 widens to i64 with the sign", `function main(): i64 { let x: i16 = -2; let y: i64 = x; return y; }`, -2},
		{"u8 widens to i16", `function main(): i16 { let x: u8 = 255; let y: i16 = x; return y + 1; }`, 256},
		{"mixed operands join at i32", `function main(): i32 { let a: u16 = 65535; let b: i16 = -1; return a + b; }`, 65534},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := evalProgramValue(t, tc.source)
			if n, ok := got.(Number); !ok || n != tc.want {
				t.Fatalf("got %v, want %v\n%s", got, tc.want, tc.source)
			}
		})
	}
}
