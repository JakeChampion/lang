package e2e

import (
	"strconv"
	"strings"
	"testing"
)

// A MapIter cursor is freed at its last use (#9562). Its box carried a static
// sentinel count, so no release could free it and every `m.iter()` — every
// `for (k, v) in m` included — stranded 32 bytes. Each case iterates eight
// times so a per-iteration leak cannot hide in the census.
var mapIterReclaimCases = []struct {
	name string
	body string
	want int
}{
	{"local", `var it = m.iter();
        while (it.has_next()) { total = total + it.value(); it.advance(); }`, 24},
	{"for-in", `for (k, v) in m { total = total + v + k.len(); }`, 48},
	{"fresh-argument", `total = total + drain(m.iter());`, 24},
	{"alias", `var it = m.iter();
        var alias = it;
        total = total + drain(alias);`, 24},
	{"returned-over-a-param", `var o = over(m);
        if (o.has_next()) { total = total + o.value(); }`, 8},
}

func mapIterReclaimSrc(body string) string {
	return `import "core/map";
function drain(it: MapIter[string, i32]): i32 {
    var t: i32 = 0;
    while (it.has_next()) { t = t + it.value(); it.advance(); }
    return t;
}
function over(m: Map[string, i32]): MapIter[string, i32] { return m.iter(); }
function main(): i32 {
    var m: Map[string, i32] = map_new(4);
    m = m.insert("a", 1);
    m = m.insert("bb", 2);
    var total: i32 = 0;
    var i: i32 = 0;
    while (i < 8) {
        ` + body + `
        i = i + 1;
    }
    return total;
}`
}

func TestMapIterCursorIsReclaimed(t *testing.T) {
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
