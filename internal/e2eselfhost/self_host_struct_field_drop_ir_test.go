package e2eselfhost

import "testing"

// TestSelfHostStructFieldDrop verifies the Perceus deep-drop of a reclaimable
// struct's scalar-array (i32[]) field. use_bag builds a Bag{items: i32[], n}
// (b is reclaimable — fresh and only field-read) and returns b.items[0] + b.n
// = 1 + 3 = 4. At b's scope exit both the array it solely owns and the struct
// box are freed. The leakcheck census pins that the field is dropped; the exit
// code pins that it is not freed twice (a double free corrupts the field read).
func TestSelfHostStructFieldDrop(t *testing.T) {
	const prog = `struct Bag { items: i32[], n: i32 }
function use_bag(): i32 {
    var b: Bag = Bag { items: [1, 2, 3], n: 3 };
    return b.items[0] + b.n;
}
function main(): i32 { return use_bag(); }
`
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			stderr, code := cli.exitOf(t, prog, target, "FERN_LEAKCHECK=1")
			if code != 4 {
				t.Fatalf("exit %d, want 4 (b.items[0] + b.n)\n%s", code, stderr)
			}
			assertBalancedCensus(t, stderr)
		})
	}
}
