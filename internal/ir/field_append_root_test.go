package ir

import (
	"strings"
	"testing"
)

// A field-place append may grow in place only when its ROOT names a box this
// frame may grow: the receiver or a parameter, or a local built from struct
// literals and fresh call results. A local bound from a field read, an
// index, another name, a match arm or a `for` element names a box some other
// live value owns, and the rc==1 grow would lengthen that value's array too
// (#8768). The verdict is read off the AppendSite the lowering records.
func TestFieldAppendRootRule(t *testing.T) {
	p := lowerSource(t, `struct Inner { xs: i32[] }
struct Outer { inner: Inner, n: i32 }
enum Box { Some1(Inner), None1 }
@noinline
function mk(): Outer {
    var b: i32[] = [];
    var i: i32 = 0;
    while (i < 3) { b = b.append(i); i = i + 1; }
    return Outer { inner: Inner { xs: b }, n: 0 };
}
@noinline
function same(o: Outer): Outer { return o; }
function param_root(o: Inner, v: i32): i32 {
    var ys: i32[] = o.xs.append(v);
    return ys.len();
}
function literal_root(v: i32): i32 {
    var t: Inner = Inner { xs: [1, 2] };
    var ys: i32[] = t.xs.append(v);
    return ys.len();
}
function fresh_call_root(v: i32): i32 {
    var o: Outer = mk();
    var ys: i32[] = o.inner.xs.append(v);
    return ys.len();
}
function field_read_root(v: i32): i32 {
    var o: Outer = mk();
    var t: Inner = o.inner;
    var ys: i32[] = t.xs.append(v);
    return ys.len() * 10 + o.inner.xs.len();
}
function alias_root(v: i32): i32 {
    var o: Outer = mk();
    var u: Outer = o;
    var ys: i32[] = u.inner.xs.append(v);
    return ys.len() * 10 + o.inner.xs.len();
}
function passthrough_call_root(v: i32): i32 {
    var o: Outer = mk();
    var u: Outer = same(o);
    var ys: i32[] = u.inner.xs.append(v);
    return ys.len() * 10 + o.inner.xs.len();
}
function match_binding_root(v: i32): i32 {
    var o: Outer = mk();
    var bx: Box = Some1(o.inner);
    match (bx) {
        Some1(t) => { var ys: i32[] = t.xs.append(v); return ys.len() * 10 + o.inner.xs.len(); },
        None1 => { return 0; }
    }
}
function main(): i32 { return 0; }`)
	want := map[string]bool{
		"param_root":            false,
		"literal_root":          false,
		"fresh_call_root":       false,
		"field_read_root":       true,
		"alias_root":            true,
		"passthrough_call_root": true,
		"match_binding_root":    true,
	}
	seen := map[string]bool{}
	for _, fn := range p.Funcs {
		copies, tracked := want[fn.Name]
		if !tracked {
			continue
		}
		seen[fn.Name] = true
		var sites []AppendSite
		for _, s := range fn.AppendSites {
			if strings.Contains(s.Recv, ".") {
				sites = append(sites, s)
			}
		}
		if len(sites) != 1 {
			t.Errorf("%s: %d field-place append sites recorded, want 1", fn.Name, len(sites))
			continue
		}
		if sites[0].Copies != copies {
			t.Errorf("%s: copies=%v (%s), want %v", fn.Name, sites[0].Copies, sites[0].Reason, copies)
		}
		if copies && !strings.Contains(sites[0].Reason, "#8768") {
			t.Errorf("%s: forced to copy for %q, want the root rule", fn.Name, sites[0].Reason)
		}
	}
	for name := range want {
		if !seen[name] {
			t.Errorf("%s was never checked — the source no longer declares it", name)
		}
	}
}
