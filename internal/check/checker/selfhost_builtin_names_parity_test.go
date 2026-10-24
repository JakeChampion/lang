package checker

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/syntax/parser"
)

func TestSelfHostKnowsEveryNativeBuiltin(t *testing.T) {
	native := nativeBuiltinNames(t)
	selfHost := selfHostBuiltinNames(t)

	var missing []string
	for _, name := range native {
		if !selfHost[name] {
			missing = append(missing, name)
		}
	}
	if len(missing) == 0 {
		return
	}
	sort.Strings(missing)
	t.Errorf("%d builtin(s) the native checker knows and the self-hosted compiler does not: %s\n"+
		"Each one compiles on the native leg and fails the self-host leg with E001 at the call site.\n"+
		"Teach compiler/parser.fern's builtin_function_names(), and lower it: ircore,\n"+
		"ir (op + extension kind id), semsource, ssarc, asmcore, and asm_ir / asm_arm64_ir / wasm_ir.\n"+
		"See #9085. Do not add an exemption here.",
		len(missing), strings.Join(missing, ", "))
}

func TestNativeKnowsEverySelfHostBuiltin(t *testing.T) {
	native := map[string]bool{}
	for _, name := range nativeBuiltinNames(t) {
		native[name] = true
	}
	var extra []string
	for name := range selfHostBuiltinNames(t) {
		if !native[name] && !strings.HasPrefix(name, "__") {
			extra = append(extra, name)
		}
	}
	if len(extra) > 0 {
		sort.Strings(extra)
		t.Errorf("%d builtin(s) the self-hosted compiler knows and the Go checker does not: %s\n"+
			"A builtin the language has is missing from the Go checker's FuncSigs, which must learn it;\n"+
			"a name the language does not have is a leftover in compiler/parser.fern's builtin_function_names().",
			len(extra), strings.Join(extra, ", "))
	}
}

func nativeBuiltinNames(t *testing.T) []string {
	t.Helper()
	const probe = `function main(): i32 { return 0; }`
	prog, err := parser.Parse(probe)
	if err != nil {
		t.Fatalf("parse probe: %v", err)
	}
	info, err := Check(prog)
	if err != nil {
		t.Fatalf("check probe: %v", err)
	}
	declared := map[string]bool{}
	for _, fn := range prog.Funcs {
		declared[fn.Name] = true
	}
	var out []string
	for name := range info.FuncSigs {
		if declared[name] || strings.HasPrefix(name, "__") {
			continue
		}
		out = append(out, name)
	}
	sort.Strings(out)
	if len(out) == 0 {
		t.Fatal("no builtins found in a checked probe — this test would pass on anything")
	}
	return out
}

var selfHostBuiltinNamesRE = regexp.MustCompile(
	`(?s)function builtin_function_names\(\): string\[\] \{.*?return \[(.*?)\n\s*\];`)

func selfHostBuiltinNames(t *testing.T) map[string]bool {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "compiler", "parser.fern"))
	if err != nil {
		t.Fatalf("read self-host parser.fern: %v", err)
	}
	m := selfHostBuiltinNamesRE.FindStringSubmatch(string(b))
	if m == nil {
		t.Fatal("cannot find builtin_function_names() in compiler/parser.fern — " +
			"the pattern no longer matches, so this test proves nothing")
	}
	body := regexp.MustCompile(`//[^\n]*`).ReplaceAllString(m[1], "")
	out := map[string]bool{}
	for _, q := range fernStringRE.FindAllStringSubmatch(body, -1) {
		out[q[1]] = true
	}
	if len(out) == 0 {
		t.Fatal("builtin_function_names() parsed to an empty list — this test would fail on everything")
	}
	return out
}
