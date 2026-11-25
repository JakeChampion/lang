package interp

import (
	"errors"
	"testing"
)

// byteBox runs main, then gives the array that the function buf returns.
func byteBox(t *testing.T, src string) Array {
	t.Helper()
	_, ip := evalProgram(t, src)
	v, err := ip.CallByName("buf", nil)
	if err != nil {
		t.Fatalf("buf: %v", err)
	}
	a, ok := v.(Array)
	if !ok {
		t.Fatalf("buf gave %T, want an array", v)
	}
	return a
}

// A u8[] from __alloc_u8 keeps one byte for each element through reads,
// writes, slices and a length change (#17245).
func TestAllocU8StaysBytes(t *testing.T) {
	src := `function buf(): u8[] {
		let b: u8[] = __alloc_u8(1 << 20);
		let n: i32 = 0;
		while (n < b.len()) {
			b = b.with(n, (n & 255) as u8);
			n = n + 4096;
		}
		let view: [u8] = b[4096:8192];
		b = b.with(1, view[0] + b[8192]);
		unsafe { __arr_set_len(b, 9000); }
		return b;
	}
	function main(): i32 { return buf().len(); }`
	a := byteBox(t, src)
	got, ok := a.Bytes()
	if !ok {
		t.Fatalf("the array widened to one Value for each element")
	}
	if len(got) != 9000 || got[0] != 0 || got[1] != 0 || got[4096] != 0 || got[8192] != 0 {
		t.Errorf("bytes [0]=%d [1]=%d [4096]=%d [8192]=%d len %d", got[0], got[1], got[4096], got[8192], len(got))
	}
}

// Each operation on a box of bytes gives what it gives on an array of Values.
func TestByteBoxMatchesValues(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want Number
	}{
		{
			name: "write in place and through a copy",
			src: `function main(): i32 {
				let a: u8[] = __alloc_u8(4);
				a = a.with(0, 7u8);
				let b: u8[] = a;
				let c: u8[] = a.with(1, 9u8);
				let a0: i32 = a[0];
				let a1: i32 = a[1];
				let b1: i32 = b[1];
				let c1: i32 = c[1];
				return a0 * 1000 + a1 * 100 + b1 * 10 + c1;
			}`,
			want: 7009,
		},
		{
			name: "append widens and keeps the bytes",
			src: `function main(): i32 {
				let a: u8[] = __alloc_u8(2).with(1, 5u8);
				let b: u8[] = a.append(6u8);
				return b.len() * 100 + b[1] * 10 + b[2];
			}`,
			want: 356,
		},
		{
			name: "regrow past the length zeroes the new bytes",
			src: `function main(): i32 {
				let a: u8[] = __alloc_u8(3).with(2, 4u8);
				unsafe { __arr_set_len(a, 2); }
				unsafe { __arr_set_len(a, 3); }
				let kept: i32 = a[2];
				unsafe { __arr_set_len(a, 6); }
				return kept * 100 + a.len() * 10 + a[5];
			}`,
			want: 460,
		},
		{
			name: "string from the bytes",
			src: `function main(): i32 {
				let a: u8[] = __alloc_u8(2).with(0, 104u8).with(1, 105u8);
				let s: string = unsafe { string_from_bytes_unchecked(a) };
				let h: i32 = s[0];
				return h * 10 + s.len();
			}`,
			want: 1042,
		},
		{
			name: "truncate",
			src: `function main(): i32 {
				let a: u8[] = __alloc_u8(5).with(1, 8u8);
				let t: u8[] = a.truncate(2);
				return t.len() * 10 + t[1];
			}`,
			want: 28,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := evalProgramValue(t, tc.src); got != tc.want {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestByteBoxReadPastTheEndAborts(t *testing.T) {
	_, ip := evalProgram(t, `function past(): i32 {
		let a: u8[] = __alloc_u8(2);
		return a[2];
	}
	function main(): i32 { return 0; }`)
	_, err := ip.CallByName("past", nil)
	var ab *Abort
	if !errors.As(err, &ab) {
		t.Fatalf("got %v, want an abort", err)
	}
	if want := indexAbort(CauseArrOOB, "2", 2).Error(); ab.Error() != want {
		t.Errorf("got %q, want %q", ab.Error(), want)
	}
}

func TestByteBoxEquality(t *testing.T) {
	hi := byteArray([]byte("hi"))
	if !valuesEqual(hi, byteArray([]byte("hi"))) || !valuesEqual(hi, arrayOf(Number('h'), Number('i'))) {
		t.Errorf("equal arrays compare unequal")
	}
	if valuesEqual(hi, byteArray([]byte("ho"))) || valuesEqual(hi, byteArray([]byte("h"))) {
		t.Errorf("unequal arrays compare equal")
	}
}
