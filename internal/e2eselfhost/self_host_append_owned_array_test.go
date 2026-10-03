package e2eselfhost

import (
	"os/exec"
	"strings"
	"testing"
)

// TestSelfHostAppendedArrayLocalIsRetainedX86_64 pins #10538: on the
// per-module driver, an owned array local appended into an array must be
// retained by the push, since the exit sweep releases the local's own
// reference. Without the retain the container held a freed buffer, which the
// sanitizer reports as a use-after-free and a real server hit as a segfault
// on its second request. Three shapes, all through the sanitizer build: the
// bare append, the append into a struct field, and a call-born element
// appended into a struct field the way std/serve grows a connection's buffer.
//
// Only the two fatal detectors are asserted. The per-module lowering releases
// an array of arrays deeply only where it proves the local fresh and
// non-escaping (slot_is_reclaimable_arrarr) and leaks the rest by design, so
// the leak census may name the elements the container now rightly holds.
func TestSelfHostAppendedArrayLocalIsRetainedX86_64(t *testing.T) {
	t.Setenv("FERN_SANITIZE", "1")
	gcc, runner, driverBin := buildModloadDriverX86(t)
	cases := []struct {
		name, src, want string
	}{
		{"bare append", `import "core/int";
function fresh(): u8[] {
    let e: u8[] = [];
    return e.append(9u8);
}
function add(xs: u8[][]): u8[][] {
    let empty: u8[] = fresh();
    return xs.append(empty);
}
function main(): i32 {
    let xs: u8[][] = [];
    xs = add(xs);
    let b: u8[] = xs[0];
    print(int.int_to_string(b.len()));
    return 0;
}
`, "1"},
		{"struct field", `import "core/int";
struct Conns { bufs: u8[][] }
function add(c: Conns): Conns {
    let empty: u8[] = [];
    return Conns { bufs: c.bufs.append(empty) };
}
function main(): i32 {
    let c: Conns = Conns { bufs: [] };
    c = add(c);
    let b: u8[] = c.bufs[0];
    print(int.int_to_string(b.len()));
    return 0;
}
`, "0"},
		{"connection table", `import "core/int";
struct Conns { fds: i32[], bufs: i32[][] }
function add(c: Conns, fd: i32): Conns {
    let empty: i32[] = [];
    return Conns { fds: c.fds.append(fd), bufs: c.bufs.append(empty) };
}
function grow(c: Conns, at: i32, chunk: i32[]): Conns {
    let buf: i32[] = c.bufs[at];
    buf = buf.append(chunk[0]);
    return Conns { ...c, bufs: c.bufs.with(at, buf) };
}
function main(): i32 {
    let c: Conns = Conns { fds: [], bufs: [] };
    let chunk: i32[] = [7];
    c = add(c, 5);
    c = grow(c, 0, chunk);
    c = add(c, 6);
    c = grow(c, 1, chunk);
    c = grow(c, 0, chunk);
    print(int.int_to_string(c.bufs[0].len() * 10 + c.bufs[1].len()));
    return 0;
}
`, "21"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			asm, progDir := compileSourceModload(t, runner, driverBin, tc.src)
			bin := buildBin(t, gcc, progDir, "probe", asm)
			out, err := exec.Command(bin).CombinedOutput()
			got := strings.TrimSpace(string(out))
			if err != nil || strings.Contains(got, "use-after-free") || strings.Contains(got, "over-release") {
				t.Fatalf("run: %v\n%s", err, out)
			}
			if first := strings.SplitN(got, "\n", 2)[0]; first != tc.want {
				t.Fatalf("printed %q, want %q\n%s", first, tc.want, out)
			}
		})
	}
}
