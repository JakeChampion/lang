package e2ecompiler

import "testing"

const mockPlatformIRPrelude = `struct MCall { name: string, args: string }
struct MPlat { sink: Cell[string] }
function mplat_new(): MPlat { return MPlat { sink: cell_new("") }; }
function (m: MPlat) record(name: string, args: string): void {
    m.sink.set(m.sink.get() + name + "\t" + args + "\n");
}
function (m: MPlat) calls(): MCall[] {
    let out: MCall[] = [];
    let log: string = m.sink.get();
    let start: i32 = 0;
    let tab: i32 = -1;
    let i: i32 = 0;
    while (i < log.len()) {
        let ch: i32 = log[i] as i32;
        if (ch == 9 && tab < 0) { tab = i; }
        if (ch == 10) {
            if (tab < 0) {
                out = out.append(MCall { name: slice_unchecked(log, start, i) + "", args: "" });
            } else {
                out = out.append(MCall {
                    name: slice_unchecked(log, start, tab) + "",
                    args: slice_unchecked(log, tab + 1, i) + ""
                });
            }
            start = i + 1;
            tab = -1;
        }
        i = i + 1;
    }
    return out;
}
function (m: MPlat) call_count(): i32 { return m.calls().len(); }
function (m: MPlat) reset(): void { m.sink.set(""); }
function (m: MPlat) has_call(name: string): boolean {
    let cs: MCall[] = m.calls();
    let i: i32 = 0;
    while (i < cs.len()) {
        if (cs[i].name == name) { return true; }
        i = i + 1;
    }
    return false;
}
function (m: MPlat) find_call(name: string): Option[MCall] {
    let cs: MCall[] = m.calls();
    let i: i32 = 0;
    while (i < cs.len()) {
        if (cs[i].name == name) { return Some(cs[i]); }
        i = i + 1;
    }
    return None;
}
`

var mockPlatformIRCases = []struct {
	name string
	main string
	want int
}{
	{"call-count", `let m: MPlat = mplat_new(); m.record("fetch", "a"); m.record("kv", "b"); m.record("fetch", "c"); return m.call_count();`, 3},
	{"indexed-field", `let m: MPlat = mplat_new(); m.record("fetch", "a"); m.record("kv", "b"); return m.calls()[1].name[0] as i32;`, 107},
	{"shared-cell", `let m: MPlat = mplat_new(); let view: MPlat = MPlat { sink: m.sink }; view.record("fetch", "a"); view.record("kv", "b"); return m.call_count();`, 2},
	{"has-call-yes", `let m: MPlat = mplat_new(); m.record("fetch", "a"); m.record("kv", "b"); if (m.has_call("kv")) { return 1; } return 0;`, 1},
	{"has-call-no", `let m: MPlat = mplat_new(); m.record("fetch", "a"); if (m.has_call("missing")) { return 1; } return 9;`, 9},
	{"find-some", `let m: MPlat = mplat_new(); m.record("fetch", "GET"); m.record("kv", "set"); match (m.find_call("fetch")) { Some(c) => { return c.args.len(); }, None => { return 0; }, } return 0;`, 3},
	{"find-none", `let m: MPlat = mplat_new(); m.record("fetch", "GET"); match (m.find_call("nope")) { Some(c) => { return 0; }, None => { return 7; }, } return 0;`, 7},
	{"reset", `let m: MPlat = mplat_new(); m.record("fetch", "a"); m.record("kv", "b"); m.reset(); return m.call_count();`, 0},
}

func mockPlatformIRSrc(mainBody string) string {
	return mockPlatformIRPrelude + "\nfunction main(): i32 { " + mainBody + " }\n"
}

func TestSelfHostMockPlatformIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "wasm32-wasi"} {
		for _, tc := range mockPlatformIRCases {
			t.Run(target+"/"+tc.name, func(t *testing.T) {
				if stderr, code := cli.exitOf(t, mockPlatformIRSrc(tc.main), target); code != tc.want {
					t.Errorf("exited %d, want %d\n%s", code, tc.want, stderr)
				}
			})
		}
	}
}
