package checker

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/stdlib"
	"github.com/jakechampion/lang/internal/syntax/parser"
)

func TestSelfHostRoutesEveryNativeBuiltin(t *testing.T) {
	prog, err := parser.Parse(`function main(): i32 { return 0; }`)
	if err != nil {
		t.Fatalf("parse probe: %v", err)
	}
	info, err := Check(prog)
	if err != nil {
		t.Fatalf("check probe: %v", err)
	}
	routed := map[string]bool{}
	for _, f := range []string{"semlower.fern", "semsource.fern", "ssarc.fern", "constfold.fern"} {
		b, err := os.ReadFile(filepath.Join("..", "..", "..", "compiler", f))
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		for _, m := range regexp.MustCompile(`"([A-Za-z_][A-Za-z0-9_]*)"`).FindAllStringSubmatch(stripLineComments(string(b)), -1) {
			routed[m[1]] = true
		}
	}
	// Default display builtins route to ordinary private runtime functions.
	// Parse their actual bodies so a missing definition fails this gate.
	for _, name := range []string{"primitive_text", "f64_text"} {
		source, err := stdlib.ReadModule("core/" + name)
		if err != nil {
			t.Fatal(err)
		}
		mod, err := parser.Parse(source.Code)
		if err != nil {
			t.Fatal(err)
		}
		for _, fn := range mod.Funcs {
			if strings.HasPrefix(fn.Name, "__primitive_") && fn.Body != nil && len(fn.Body.Stmts) > 0 {
				routed[fn.Name] = true
			}
		}
	}
	var missing, nowRouted []string
	for name := range info.FuncSigs {
		if name == "main" || strings.HasPrefix(name, "__method_") || strings.Contains(name, "__assoc_") {
			continue
		}
		_, nativeOnly := nativeOnlyBuiltins[name]
		switch {
		case !routed[name] && !nativeOnly:
			missing = append(missing, name)
		case routed[name] && nativeOnly:
			nowRouted = append(nowRouted, name)
		}
	}
	sort.Strings(missing)
	sort.Strings(nowRouted)
	if len(missing) > 0 {
		t.Errorf("%d builtin(s) native lowers that no self-host lowering routes: %s\n"+
			"A program calling one type-checks and then fails to compile on the self-host.",
			len(missing), strings.Join(missing, ", "))
	}
	if len(nowRouted) > 0 {
		t.Errorf("nativeOnlyBuiltins lists %s, which the self-host now routes: remove the row(s)",
			strings.Join(nowRouted, ", "))
	}
}

var nativeOnlyBuiltins = map[string]string{
	"__alloc_reuse": "native's Perceus reuse token, which only native's own reuse lowering emits",
	"__rc_get":      "an rc inspection intrinsic native's rc tests call; no self-host lowering handles it",
}

func stripLineComments(src string) string {
	var out strings.Builder
	for _, line := range strings.Split(src, "\n") {
		inStr := false
		cut := len(line)
		for i := 0; i < len(line); i++ {
			switch {
			case inStr && line[i] == '\\':
				i++
			case !inStr && line[i] == '\'':
				for i++; i < len(line) && line[i] != '\''; i++ {
					if line[i] == '\\' {
						i++
					}
				}
			case line[i] == '"':
				inStr = !inStr
			case !inStr && line[i] == '/' && i+1 < len(line) && line[i+1] == '/':
				cut = i
				i = len(line)
			}
		}
		out.WriteString(line[:cut])
		out.WriteByte('\n')
	}
	return out.String()
}
