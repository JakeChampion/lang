package checker

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/parser"
)

// Two name lists in examples/self_host/checker.fern mirror a native table and
// are pinned to it entry for entry here: registered_intrinsics, the `__` names
// a free function may not take (E006), and fip_builtin_callees' literal, the
// builtins a `fip` function may call (fipNonAllocBuiltins, #9607). A name
// missing on either side is a program the two checkers disagree on.

var quotedNameRE = regexp.MustCompile(`"([A-Za-z_][A-Za-z0-9_]*)"`)

// selfHostNameList returns the quoted names in the first `[ ... ]` after the
// declaration of `fn` in the self-host checker.
func selfHostNameList(t *testing.T, fn string) []string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "examples", "self_host", "checker.fern"))
	if err != nil {
		t.Fatalf("read self-host checker.fern: %v", err)
	}
	src := string(b)
	at := strings.Index(src, "function "+fn+"(")
	if at < 0 {
		t.Fatalf("cannot find %s in examples/self_host/checker.fern, so this test proves nothing", fn)
	}
	open := strings.Index(src[at:], "[\n")
	if alt := strings.Index(src[at:], "in ["); alt >= 0 && (open < 0 || alt < open) {
		open = alt + 3
	}
	end := strings.Index(src[at+open:], "]")
	if open < 0 || end < 0 {
		t.Fatalf("cannot find %s's name list", fn)
	}
	var out []string
	for _, m := range quotedNameRE.FindAllStringSubmatch(src[at+open:at+open+end], -1) {
		out = append(out, m[1])
	}
	if len(out) == 0 {
		t.Fatalf("%s's list parsed empty, so this test would fail on everything", fn)
	}
	sort.Strings(out)
	return out
}

func sameNames(t *testing.T, what string, native, selfHost []string) {
	t.Helper()
	sort.Strings(native)
	if strings.Join(native, ",") != strings.Join(selfHost, ",") {
		t.Errorf("%s differ:\n  native:    %s\n  self-host: %s", what, strings.Join(native, " "), strings.Join(selfHost, " "))
	}
}

func TestSelfHostRegisteredIntrinsicsMatch(t *testing.T) {
	prog, err := parser.Parse(`function main(): i32 { return 0; }`)
	if err != nil {
		t.Fatalf("parse probe: %v", err)
	}
	info, err := Check(prog)
	if err != nil {
		t.Fatalf("check probe: %v", err)
	}
	var native []string
	for name := range info.FuncSigs {
		if strings.HasPrefix(name, "__") && !strings.HasPrefix(name, "__method_") {
			native = append(native, name)
		}
	}
	sameNames(t, "registered intrinsics", native, selfHostNameList(t, "registered_intrinsics"))
}

func TestSelfHostFipBuiltinCalleesMatch(t *testing.T) {
	var native []string
	for name := range fipNonAllocBuiltins {
		native = append(native, name)
	}
	sameNames(t, "fip builtin callees", native, selfHostNameList(t, "fip_builtin_callees"))
}
