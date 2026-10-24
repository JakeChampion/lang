package e2ecompiler

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var assocTypeCases = []struct {
	name string
	src  string
}{
	{
		name: "resolves to the impl's binding",
		src: `trait Holder {
    type Item;
    function get(self: Self): Self::Item;
}
struct IntBox { v: i32 }
impl Holder for IntBox {
    type Item = i32;
    function get(self: Self): Self::Item { return self.v; }
}
function main(): i32 {
    let b: IntBox = IntBox { v: 7 };
    return b.get();
}`,
	},
	{
		name: "resolution is observable at a use site",
		src: `trait Holder {
    type Item;
    function get(self: Self): Self::Item;
}
struct IntBox { v: i32 }
impl Holder for IntBox {
    type Item = i32;
    function get(self: Self): Self::Item { return self.v; }
}
function main(): i32 {
    let b: IntBox = IntBox { v: 7 };
    let s: string = b.get();
    return 0;
}`,
	},
	{
		name: "impl must bind every associated type",
		src: `trait Holder {
    type Item;
    function get(self: Self): Self::Item;
}
struct IntBox { v: i32 }
impl Holder for IntBox {
    function get(self: Self): Self::Item { return self.v; }
}
function main(): i32 { return 0; }`,
	},
	{
		name: "impl may not bind one the trait does not declare",
		src: `trait Holder {
    type Item;
    function get(self: Self): Self::Item;
}
struct IntBox { v: i32 }
impl Holder for IntBox {
    type Item = i32;
    type Extra = i32;
    function get(self: Self): Self::Item { return self.v; }
}
function main(): i32 { return 0; }`,
	},
	{
		name: "projection through a bounded generic",
		src: `trait Holder {
    type Item;
    function get(self: Self): Self::Item;
}
struct IntBox { v: i32 }
impl Holder for IntBox {
    type Item = i32;
    function get(self: Self): Self::Item { return self.v; }
}
function first[H: Holder](h: H): H::Item { return h.get(); }
function main(): i32 {
    let b: IntBox = IntBox { v: 7 };
    return first(b);
}`,
	},
	{
		name: "unpinned associated type is not object-safe",
		src: `trait Holder {
    type Item;
    function get(self: Self): Self::Item;
}
struct IntBox { v: i32 }
impl Holder for IntBox {
    type Item = i32;
    function get(self: Self): Self::Item { return self.v; }
}
function take(h: dyn Holder): i32 { return 0; }
function main(): i32 { return 0; }`,
	},
	{
		name: "pinning the associated type restores object safety",
		src: `trait Holder {
    type Item;
    function get(self: Self): Self::Item;
}
struct IntBox { v: i32 }
impl Holder for IntBox {
    type Item = i32;
    function get(self: Self): Self::Item { return self.v; }
}
function take(h: dyn Holder[Item = i32]): i32 { return 0; }
function main(): i32 { return 0; }`,
	},
	{
		name: "a pinned dyn array element is object-safe",
		src: `trait Holder {
    type Item;
    function get(self: Self): Self::Item;
}
struct IntBox { v: i32 }
impl Holder for IntBox {
    type Item = i32;
    function get(self: Self): Self::Item { return self.v; }
}
function take(hs: dyn Holder[Item = i32][]): dyn Holder[Item = i32][] { return hs; }
function main(): i32 {
    let hs: dyn Holder[Item = i32][] = [IntBox { v: 4 }];
    return take(hs).len();
}`,
	},
	{
		name: "an unpinned dyn array element is not object-safe",
		src: `trait Holder {
    type Item;
    function get(self: Self): Self::Item;
}
struct IntBox { v: i32 }
impl Holder for IntBox {
    type Item = i32;
    function get(self: Self): Self::Item { return self.v; }
}
function main(): i32 {
    let hs: dyn Holder[] = [IntBox { v: 4 }];
    return 0;
}`,
	},
	{
		name: "parametric impl binds its own type parameter",
		src: `trait Carrier {
    type Ok;
    function get(self: Self): Self::Ok;
}
struct Box[T] { v: T }
impl[T] Carrier for Box[T] {
    type Ok = T;
    function get(self: Self): Self::Ok { return self.v; }
}
function main(): i32 {
    let b: Box[i32] = Box { v: 7 };
    return b.get();
}`,
	},
	{
		name: "parametric binding is observable at a use site",
		src: `trait Carrier {
    type Ok;
    function get(self: Self): Self::Ok;
}
struct Box[T] { v: T }
impl[T] Carrier for Box[T] {
    type Ok = T;
    function get(self: Self): Self::Ok { return self.v; }
}
function main(): i32 {
    let b: Box[i32] = Box { v: 7 };
    let s: string = b.get();
    return 0;
}`,
	},
	{
		name: "parametric impl must bind the trait's associated type",
		src: `trait Carrier {
    type Ok;
    function get(self: Self): Self::Ok;
}
struct Box[T] { v: T }
impl[T] Carrier for Box[T] {
    function get(self: Self): Self::Ok { return self.v; }
}
function main(): i32 { return 0; }`,
	},
}

func TestSelfHostAssocTypesDifferential(t *testing.T) {
	interpBin := buildLangBinForInterp(t)
	driver, err := filepath.Abs("../../../compiler/drivers/checker_codes_run.fern")
	if err != nil {
		t.Fatalf("abs driver path: %v", err)
	}

	for _, tc := range assocTypeCases {
		t.Run(tc.name, func(t *testing.T) {
			var goMsgs []string
			for _, d := range goCheckerDiags(t, t.TempDir(), tc.src) {
				goMsgs = append(goMsgs, d.code+": "+d.msg)
			}

			cmd := exec.Command(interpBin, "-interp", driver)
			cmd.Stdin = strings.NewReader(tc.src)
			out, _ := cmd.Output()
			var shMsgs []string
			for _, d := range driverDiags(string(out)) {
				shMsgs = append(shMsgs, d.code+": "+d.msg)
			}

			if !equalStrings(assocOnly(goMsgs), assocOnly(shMsgs)) {
				t.Errorf("the two checkers disagree on the associated-type diagnostics.\nnative:    %s\nself-host: %s",
					joinOrNone(assocOnly(goMsgs)), joinOrNone(assocOnly(shMsgs)))
			}
			if (len(goMsgs) == 0) != (len(shMsgs) == 0) {
				t.Errorf("one checker accepts this program and the other rejects it.\nnative:    %s\nself-host: %s",
					joinOrNone(goMsgs), joinOrNone(shMsgs))
			}
			if !equalStrings(goMsgs, shMsgs) && !onlyCascadeSurplus(goMsgs, shMsgs) {
				t.Errorf("the two checkers disagree beyond the known unknown-nominal cascade.\nnative:    %s\nself-host: %s",
					joinOrNone(goMsgs), joinOrNone(shMsgs))
			}
		})
	}
}

func joinOrNone(ms []string) string {
	if len(ms) == 0 {
		return "<no diagnostics>"
	}
	return strings.Join(ms, "\n           ")
}

func assocOnly(ms []string) []string {
	var out []string
	for _, m := range ms {
		if strings.Contains(m, "associated type") {
			out = append(out, m)
		}
	}
	return out
}

func onlyCascadeSurplus(goMsgs, shMsgs []string) bool {
	have := map[string]int{}
	for _, m := range shMsgs {
		have[m]++
	}
	for _, m := range goMsgs {
		if have[m] > 0 {
			have[m]--
			continue
		}
		if !strings.HasPrefix(m, "E002: return value: expected") {
			return false
		}
	}
	for _, n := range have {
		if n > 0 {
			return false
		}
	}
	return true
}
