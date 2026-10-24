package e2e

import (
	"strconv"
	"strings"
	"testing"
)

var mapIterReclaimCases = []struct {
	name string
	body string
	want int
}{
	{"local", `let it = m.iter();
        for (__mapkey_it, __mapvalue_it) in it { total = total + __mapvalue_it;  }`, 24},
	{"for-in", `for (k, v) in m { total = total + v + k.len(); }`, 48},
	{"fresh-argument", `total = total + drain(m.iter());`, 24},
	{"alias", `let it = m.iter();
        let alias = it;
        total = total + drain(alias);`, 24},
	{"returned-over-a-param", `let o = over(m);
        if let Some(__mapstep_o) = o.next() { let __mapkey_o = __mapstep_o.0.0; let __mapvalue_o = __mapstep_o.0.1; total = total + __mapvalue_o; }`, 8},
}

func mapIterReclaimSrc(body string) string {
	return `import "core/map"; import "core/iter";
function drain(it: iter.ArrayIter[(string, i32)]): i32 {
    let t: i32 = 0;
    for (__mapkey_it, __mapvalue_it) in it { t = t + __mapvalue_it;  }
    return t;
}
function over(m: Map[string, i32]): iter.ArrayIter[(string, i32)] { return m.iter(); }
function main(): i32 {
    let m: Map[string, i32] = map_new(4);
    m = m.insert("a", 1);
    m = m.insert("bb", 2);
    let total: i32 = 0;
    let i: i32 = 0;
    while (i < 8) {
        ` + body + `
        i = i + 1;
    }
    return total;
}`
}

func TestMapIteratorIsReclaimed(t *testing.T) {
	for _, tc := range mapIterReclaimCases {
		src := mapIterReclaimSrc(tc.body)
		t.Run(tc.name+"/x86-64", func(t *testing.T) {
			_, stderr, exit := runLeakCheckX86_64(t, src)
			checkMapReceiverCensus(t, tc.want, stderr, exit)
		})
		t.Run(tc.name+"/arm64", func(t *testing.T) {
			_, stderr, exit := runLeakCheckArm64(t, src)
			checkMapReceiverCensus(t, tc.want, stderr, exit)
		})
		t.Run(tc.name+"/wasm", func(t *testing.T) {
			stdout, stderr, exit := runLeakCheckWasm(t, src, false)
			if exit != 0 || strings.TrimSpace(stdout) != strconv.Itoa(tc.want) {
				t.Fatalf("exit %d stdout %q, want exit 0 printing %d; stderr: %s", exit, stdout, tc.want, stderr)
			}
			a, f, live := parseWasmLeakCheckLine(t, stderr)
			if a != f || live != 0 {
				t.Errorf("allocs=%d frees=%d live_bytes=%d, want a balanced census", a, f, live)
			}
		})
	}
}
