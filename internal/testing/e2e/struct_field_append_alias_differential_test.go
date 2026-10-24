package e2e

import "testing"

func TestStructFieldAppendAliasDifferential(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping struct-field append-alias differential in -short mode")
	}
	cases := []struct {
		name, src string
	}{
		{"generic_box_cow_shared", `import "std/i32";
struct GBox[T] { xs: T[], }
function gbox_new[T](): GBox[T] { return GBox { xs: [] }; }
pub function (b: GBox[T]) push[T](x: T): GBox[T] { let ys: T[] = b.xs.append(x); return GBox { xs: ys }; }
pub function (b: GBox[T]) size(): i32 { return b.xs.len(); }
function main(): i32 {
    let a: GBox[i32] = gbox_new();
    a = a.push(1); a = a.push(2);
    let before: i32 = a.size();       // 2
    let c: GBox[i32] = a.push(3);     // must NOT mutate a
    let after: i32 = a.size();        // still 2
    print((before * 10 + after).to_string());
    return 0;
}`},
		{"mono_box_cow_shared", `import "std/i32";
struct Box { xs: i32[], }
function box_new(): Box { return Box { xs: [] }; }
pub function (b: Box) push(x: i32): Box { let ys: i32[] = b.xs.append(x); return Box { xs: ys }; }
pub function (b: Box) size(): i32 { return b.xs.len(); }
function main(): i32 {
    let a: Box = box_new();
    a = a.push(1); a = a.push(2);
    let before: i32 = a.size();
    let c: Box = a.push(3);
    let after: i32 = a.size();
    print((before * 10 + after).to_string());
    return 0;
}`},
		{"two_derived_live", `import "std/i32";
struct Box { xs: i32[], }
function box_new(): Box { return Box { xs: [] }; }
pub function (b: Box) push(x: i32): Box { let ys: i32[] = b.xs.append(x); return Box { xs: ys }; }
pub function (b: Box) size(): i32 { return b.xs.len(); }
function main(): i32 {
    let a: Box = box_new();
    a = a.push(1); a = a.push(2);
    let c: Box = a.push(3);
    let d: Box = a.push(4);
    print((a.size() * 100 + c.size() * 10 + d.size()).to_string());
    return 0;
}`},
		{"derived_contents_correct", `import "std/i32";
struct Box { xs: i32[], }
function box_new(): Box { return Box { xs: [] }; }
pub function (b: Box) push(x: i32): Box { let ys: i32[] = b.xs.append(x); return Box { xs: ys }; }
pub function (b: Box) sum(): i32 { let s: i32 = 0; let i: i32 = 0; while (i < b.xs.len()) { s = s + b.xs[i]; i = i + 1; } return s; }
function main(): i32 {
    let a: Box = box_new();
    a = a.push(1); a = a.push(2);
    let c: Box = a.push(3);
    print((c.sum() * 10 + a.sum()).to_string());   // 6*10 + 3 = 63
    return 0;
}`},
		{"literal_built_guard", `import "std/i32";
struct Box { xs: i32[], }
pub function (b: Box) push(x: i32): Box { let ys: i32[] = b.xs.append(x); return Box { xs: ys }; }
pub function (b: Box) size(): i32 { return b.xs.len(); }
function main(): i32 {
    let a: Box = Box { xs: [1, 2] };
    let before: i32 = a.size();
    let c: Box = a.push(3);
    let after: i32 = a.size();
    print((before * 10 + after).to_string());
    return 0;
}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertNumProgramAgrees(t, tc.src)
		})
	}
}
