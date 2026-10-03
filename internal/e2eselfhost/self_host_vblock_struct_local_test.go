package e2eselfhost

import "testing"

// A struct local initialised by a value block that is nothing but its tail
// (`let h: Holder = { Holder { .. } }`) is as fresh as the flat literal and is
// reclaimed the same way (#10610). Answers are the interpreter's.

const vblockHolder = `struct Inst { name: string, depth: i32 }
struct Holder { x: Inst }
`

const vblockHolderMain = `function main(): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < 10) { t = t + round(i); i = i + 1; }
    return t % 100;
}
`

var vblockStructLocalRows = []leakRow{
	{"flat", vblockHolder + `function round(n: i32): i32 {
    let h: Holder = Holder { x: Inst { name: "w" + "", depth: n } };
    return h.x.depth + h.x.name.len();
}
` + vblockHolderMain, true},
	{"block_tail", vblockHolder + `function round(n: i32): i32 {
    let h: Holder = { Holder { x: Inst { name: "w" + "", depth: n } } };
    return h.x.depth + h.x.name.len();
}
` + vblockHolderMain, true},
	{"block_tail_lent", vblockHolder + `function depth_of(h: Holder): i32 { return h.x.depth; }
function round(n: i32): i32 {
    let h: Holder = { Holder { x: Inst { name: "w" + "", depth: n } } };
    return depth_of(h) + h.x.name.len();
}
` + vblockHolderMain, true},
	// A block that declares a local before its tail is not widened: the value
	// block's credit view drops the tail, so a block local stored into the
	// struct would read as unescaped and be freed under the holder.
	{"block_with_local", vblockHolder + `function round(n: i32): i32 {
    let h: Holder = { let i: Inst = Inst { name: "w" + "", depth: n }; Holder { x: i } };
    return h.x.depth + h.x.name.len();
}
` + vblockHolderMain, false},
}

func TestSelfHostVblockStructLocalX86_64(t *testing.T) {
	runLeakRowsX86_64(t, vblockStructLocalRows)
}

func TestSelfHostVblockStructLocalArm64(t *testing.T) {
	runLeakRowsArm64(t, vblockStructLocalRows)
}

func TestSelfHostVblockStructLocalWasm(t *testing.T) {
	runLeakRowsWasm(t, vblockStructLocalRows)
}
