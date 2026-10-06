package e2e

import "testing"

// TestArgsArrayRcHeader pins the `args()` array's refcount header on every
// target (#7969). The array carries cap / rc / len at data-12 / -8 / -4, and
// each call hands its caller a counted array to drop. A header written wrong
// shows as an over-release counted at a walk's scope exit (exit 1), or as the
// second walk reading argv strings out of a block an earlier drop freed and
// the churn loop recycled (exit 2 or 3). It holds at any argc: with no
// arguments both walks see 0 elements, but a freed header block would still
// be handed back to the churn loop.
const argsRcHeaderProgram = `function walk(): i32 {
    let n: i32 = 0;
    for a in args() { n = n + a.len(); }
    return n;
}

function main(): i32 {
    let first: i32 = walk();
    // Allocate hard between the walks: a block an early drop freed gets
    // handed straight back out here.
    let churn: i32 = 0;
    let i: i32 = 0;
    while (i < 64) {
        let buf: i32[] = [i, i + 1, i + 2, i + 3, i + 4, i + 5, i + 6, i + 7];
        churn = churn + buf[0];
        i = i + 1;
    }
    let second: i32 = walk();
    if (first != second) { return 2; }
    if (args().len() != first_len()) { return 3; }
    return __rc_underflow_count();
}

function first_len(): i32 {
    return args().len();
}`

func TestArgsArrayRcHeader(t *testing.T) {
	t.Run("wasm32-wasi", func(t *testing.T) {
		if code := compileAndRunWasmbinMain(t, argsRcHeaderProgram); code != 0 {
			t.Errorf("exit %d, want 0: the args() array's rc header is wrong "+
				"(1 = over-release counted, 2/3 = the array was freed and recycled)", code)
		}
	})
	t.Run("x86_64", func(t *testing.T) {
		if _, code := compileAndRunX86_64(t, argsRcHeaderProgram); code != 0 {
			t.Errorf("exit %d, want 0: the args() array's rc header is wrong "+
				"(1 = over-release counted, 2/3 = the array was freed and recycled)", code)
		}
	})
	t.Run("arm64-linux", func(t *testing.T) {
		if _, code := compileAndRunArm64(t, argsRcHeaderProgram); code != 0 {
			t.Errorf("exit %d, want 0: the args() array's rc header is wrong "+
				"(1 = over-release counted, 2/3 = the array was freed and recycled)", code)
		}
	})
}
