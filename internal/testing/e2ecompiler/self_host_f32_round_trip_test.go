package e2ecompiler

import "testing"

// An f32 copy and an f32 constant store take no conversion through f64, and a
// signalling NaN keeps its bits through a copy.
const f32RoundTripSrc = `@noinline function copy(own dst: f32[], src: [f32]): f32[] {
    for i in 0..src.len() { dst = dst.with(i, src[i]); }
    return dst;
}
@noinline function clear(own a: f32[]): f32[] {
    for i in 0..a.len() { a = a.with(i, 0.0f32); }
    return a;
}
@noinline function fill(own a: f32[]): f32[] {
    for i in 0..a.len() { a = a.with(i, -1.5f32); }
    return a;
}
function main(): i32 {
    let src: f32[] = [1.25f32, f32_from_bits(2141192192), -0.0f32];
    let dst = copy([0.0f32, 0.0f32, 0.0f32], src);
    let r = 0;
    if (dst[0] == 1.25f32) { r = r + 1; }
    if (f32_bits(dst[1]) == 2141192192) { r = r + 2; }
    if (f32_bits(dst[2]) == f32_bits(-0.0f32)) { r = r + 4; }
    let z = clear(dst);
    if (f32_bits(z[0]) == 0 && f32_bits(z[1]) == 0 && f32_bits(z[2]) == 0) { r = r + 8; }
    let m = fill(z);
    if (m[0] == -1.5f32 && m[2] == -1.5f32) { r = r + 16; }
    return r;
}`

func TestSelfHostF32RoundTripFoldsX86_64(t *testing.T) {
	runX86ShapeCases(t, []shapeCase{
		{
			name: "f32",
			src:  f32RoundTripSrc,
			want: 31,
			lacks: map[string][]string{
				"copy":  {`cvtss2sd`, `cvtsd2ss`},
				"clear": {`cvtss2sd`, `cvtsd2ss`},
			},
		},
	})
}

func TestSelfHostF32RoundTripFoldsArm64(t *testing.T) {
	runArm64ShapeCases(t, []shapeCase{
		{
			name: "f32",
			src:  f32RoundTripSrc,
			want: 31,
			lacks: map[string][]string{
				"copy":  {`fcvt`},
				"clear": {`fcvt`},
			},
		},
	})
}
