package interp

import "testing"

// Every holder of an array shares one box, as compiled code does, so
// __arr_set_len reaches them all; a view keeps its own length.
func TestArrSetLenReachesEveryHolder(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want Number
	}{
		{
			name: "alias, field and parameter",
			src: `struct Holder { b: u8[] }
			function shrink(b: u8[]): void { __arr_set_len(b, 2); }
			function main(): i32 {
				let buf: u8[] = [1u8, 2u8, 3u8, 4u8];
				let alias: u8[] = buf;
				let h: Holder = Holder { b: buf };
				__arr_set_len(buf, 3);
				let first: i32 = buf.len() * 100 + alias.len() * 10 + h.b.len();
				shrink(alias);
				let second: i32 = buf.len() * 100 + alias.len() * 10 + h.b.len();
				__arr_set_len(h.b, 4);
				return first * 10000 + second * 10 + alias.len();
			}`,
			want: 3332224,
		},
		{
			name: "a view keeps its length and a regrow its bytes",
			src: `function main(): i32 {
				let buf: u8[] = [1u8, 2u8, 3u8, 4u8];
				let view: [u8] = buf[0:4];
				__arr_set_len(buf, 1);
				let seen: i32 = view.len() * 10 + view[3];
				__arr_set_len(buf, 4);
				return seen * 100 + buf[2] * 10 + buf[3];
			}`,
			want: 4434,
		},
		{
			name: "regrowing past an append that shared the storage",
			src: `function main(): i32 {
				let a: u8[] = [1u8];
				let b: u8[] = a.append(2u8);
				let c: u8[] = b.append(3u8);
				__arr_set_len(b, 3);
				return b[2] * 10 + c[2];
			}`,
			want: 3,
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
