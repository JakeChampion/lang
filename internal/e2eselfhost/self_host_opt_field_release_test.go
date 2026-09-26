package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A struct counts the box of an Option / Result field the way it counts a
// direct enum field's: a non-fresh store retains it and __struct_drop_<T>
// releases it shallow. The AST lowering (FERN_SEM_IR=) kept every such box
// before (#10299). Each program runs 100 rounds and answers 99 if any release
// ran past zero, so an over-release fails on the exit code as well as under
// the sanitizer. A `balanced` row must also leave a balanced census; the
// others still keep the claim of the owner that is not the struct (#10301).
const optFieldMain = `function main(): i32 {
    var t: i32 = 0;
    var r: i32 = 0;
    while (r < 100) { t = t + round(r); r = r + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 97;
}
`

const optFieldDecls = `struct H { o: Option[i32], n: i32 }
struct R { r: Result[i32, i32] }
function k_of(x: i32): i32 { return x; }
`

var optFieldCases = []struct {
	name     string
	src      string
	want     int
	balanced bool
}{
	{"issue_repro", `function round(i: i32): i32 {
    var h: H = H { o: Some(k_of(i)), n: 1 };
    var r: i32 = 0;
    match (h.o) { Some(w) => { r = w; }, None => { r = 1; } }
    return r;
}
`, 4950 % 97, true},
	{"user_get_method", `function (h: H) get(k: i32): Option[i32] { return h.o; }
function round(i: i32): i32 {
    var h: H = H { o: Some(k_of(i)), n: 1 };
    var g: Option[i32] = h.get(1);
    var r: i32 = 0;
    match (g) { Some(v) => { r = v; }, None => { r = 1; } }
    match (h.o) { Some(w) => { r = r + w; }, None => { r = r + 1; } }
    return r;
}
`, 9900 % 97, false},
	{"local_shared_into_field", `function round(i: i32): i32 {
    var o: Option[i32] = Some(k_of(i));
    var h: H = H { o: o, n: 2 };
    var r: i32 = h.n;
    match (o) { Some(v) => { r = r + v; }, None => { r = r + 1; } }
    return r;
}
`, (200 + 4950) % 97, false},
	{"field_read_outlives_rebind", `function round(i: i32): i32 {
    var h: H = H { o: Some(k_of(i)), n: 1 };
    var g: Option[i32] = h.o;
    h = H { o: None, n: 2 };
    var r: i32 = h.n;
    match (g) { Some(v) => { r = r + v; }, None => { r = r + 100; } }
    return r;
}
`, (200 + 4950) % 97, true},
	{"returned_field_outlives_rebind", `function take(h: H): Option[i32] { return h.o; }
function round(i: i32): i32 {
    var h: H = H { o: Some(k_of(i)), n: 1 };
    var g: Option[i32] = take(h);
    h = H { o: Some(k_of(3)), n: 2 };
    var r: i32 = h.n;
    match (g) { Some(v) => { r = r + v; }, None => { r = r + 100; } }
    return r;
}
`, (200 + 4950) % 97, false},
	{"spread_copy", `function round(i: i32): i32 {
    var h: H = H { o: Some(k_of(i)), n: 1 };
    var h2: H = H { ...h, n: 5 };
    var r: i32 = h2.n;
    match (h2.o) { Some(v) => { r = r + v; }, None => { r = r + 100; } }
    match (h.o) { Some(v) => { r = r + v; }, None => { r = r + 100; } }
    return r;
}
`, (500 + 2*4950) % 97, true},
	{"field_read_into_array", `function round(i: i32): i32 {
    var h: H = H { o: Some(k_of(i)), n: 1 };
    var xs: Option[i32][] = [h.o];
    h = H { o: None, n: 2 };
    var r: i32 = h.n;
    match (xs[0]) { Some(v) => { r = r + v; }, None => { r = r + 100; } }
    return r;
}
`, (200 + 4950) % 97, false},
	{"result_field", `function round(i: i32): i32 {
    var a: R = R { r: Ok(k_of(i)) };
    var b: R = R { r: Err(k_of(2)) };
    var t: i32 = 0;
    match (a.r) { Ok(v) => { t = t + v; }, Err(e) => { t = t + e; } }
    match (b.r) { Ok(v) => { t = t + v; }, Err(e) => { t = t + e; } }
    return t;
}
`, (4950 + 200) % 97, true},
}

func TestSelfHostOptFieldReleaseX86_64(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, tc := range optFieldCases {
		t.Run(tc.name, func(t *testing.T) {
			src := filepath.Join(t.TempDir(), tc.name+".fern")
			if err := os.WriteFile(src, []byte(optFieldDecls+tc.src+optFieldMain), 0o644); err != nil {
				t.Fatal(err)
			}
			for _, sem := range []string{"FERN_SEM_IR=", "FERN_SEM_IR=1"} {
				for _, mode := range []string{"FERN_LEAKCHECK=1", "FERN_SANITIZE=1"} {
					stderr, exit := runWithStdin(t, cli.runner, cli.x86Binary(t, src, mode, sem), nil)
					if exit != tc.want || sanitizerFault(stderr, tc.balanced) {
						t.Fatalf("%s %s: exit = %d, want %d (99 = rc underflow), and no sanitizer fault\n%s", sem, mode, exit, tc.want, stderr)
					}
					if mode == "FERN_LEAKCHECK=1" && (tc.balanced || sem == "FERN_SEM_IR=1") {
						assertBalancedCensus(t, stderr)
					}
				}
			}
		})
	}
}

// sanitizerFault: a sanitizer report other than a leak, or any report at all
// when the row must balance.
func sanitizerFault(stderr string, balanced bool) bool {
	for _, line := range strings.Split(stderr, "\n") {
		if strings.HasPrefix(line, "fern-sanitizer:") && (balanced || !strings.HasPrefix(line, "fern-sanitizer: leak ")) {
			return true
		}
	}
	return false
}

// TestSelfHostOptFieldReleaseWasm runs the same rows on wasm, whose rebind
// path releases a replaced Option field through its own arm.
func TestSelfHostOptFieldReleaseWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}
	cli := buildSelfHostCLI(t)
	for _, tc := range optFieldCases {
		t.Run(tc.name, func(t *testing.T) {
			src := filepath.Join(t.TempDir(), tc.name+".fern")
			if err := os.WriteFile(src, []byte(optFieldDecls+tc.src+optFieldMain), 0o644); err != nil {
				t.Fatal(err)
			}
			for _, sem := range []string{"FERN_SEM_IR=", "FERN_SEM_IR=1"} {
				stderr, exit := runWasmCensus(t, cli.emit(t, src, "wasm32-wasi", "FERN_LEAKCHECK=1", sem))
				if exit != tc.want {
					t.Fatalf("%s: exit = %d, want %d (99 = rc underflow)\n%s", sem, exit, tc.want, stderr)
				}
				if tc.balanced || sem == "FERN_SEM_IR=1" {
					assertBalancedCensus(t, stderr)
				}
			}
		})
	}
}
