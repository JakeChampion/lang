package checker

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// TestSelfHostContractsEveryBuiltin is the typed path's half of
// TestSelfHostBuiltinSigsMatch. A builtin the self-host checker types but the
// semantic boundary has no contract for refuses every function calling it, and
// the module falls back to the AST lowering; a contract with no op in ssarc
// lowers the call as a direct call to a symbol nothing defines, and is refused
// the same way. Either shows only under FERN_SEM_IR_STRICT. udp_send,
// wasm_block and wasm_poll were all three.
//
// A name counts as handled when semsource.fern names it (a contract row or an
// arm of its own) or constfold.fern folds it before the boundary sees it. The
// `__` intrinsics are TestSelfHostTypesEveryIntrinsicFamily's.
func TestSelfHostContractsEveryBuiltin(t *testing.T) {
	read := func(name string) string {
		b, err := os.ReadFile(filepath.Join("..", "..", "examples", "self_host", name))
		if err != nil {
			t.Fatalf("read self-host %s: %v", name, err)
		}
		return string(b)
	}
	semsource := read("semsource.fern")
	constfold := read("constfold.fern")
	ssarc := read("ssarc.fern")
	quoted := func(src, name string) bool { return strings.Contains(src, `"`+name+`"`) }

	contracted := map[string]bool{}
	for _, m := range regexp.MustCompile(`Contract \{ name: "([a-z_0-9]+)"`).FindAllStringSubmatch(semsource, -1) {
		contracted[m[1]] = true
	}
	if len(contracted) == 0 {
		t.Fatal("found no contract rows in semsource.fern — the pattern no longer matches, so this test proves nothing")
	}

	var unhandled, unlowered []string
	for name := range selfHostBuiltinSigRows(t) {
		if strings.HasPrefix(name, "__") {
			continue
		}
		if !quoted(semsource, name) && !quoted(constfold, name) {
			unhandled = append(unhandled, name)
		}
		if contracted[name] && !quoted(ssarc, name) {
			unlowered = append(unlowered, name)
		}
	}
	sort.Strings(unhandled)
	sort.Strings(unlowered)
	if len(unhandled) > 0 {
		t.Errorf("%d builtin(s) the self-host checker types have no semantic contract in semsource.fern: %s",
			len(unhandled), strings.Join(unhandled, ", "))
	}
	if len(unlowered) > 0 {
		t.Errorf("%d contracted builtin(s) have no op in ssarc.fern, so a call lowers to an undefined symbol: %s",
			len(unlowered), strings.Join(unlowered, ", "))
	}
}
