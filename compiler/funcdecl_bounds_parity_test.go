package compiler

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

var selfHostSources = []string{
	"parser.fern",
	"irtables.fern",
	"lift.fern",
	"constfold.fern",
}

var carriesTypeParams = regexp.MustCompile(`type_params:\s*\w+\.type_params`)

var clearsTypeParams = regexp.MustCompile(`type_params:\s*(\[\]|\w+)(?:,|\s|$)`)

var carriesBoundTraits = regexp.MustCompile(`bound_traits:\s*\w+\.bound_traits`)

var typeParamsLocal = regexp.MustCompile(`type_params:\s*(\w+)\s*[,}]`)

var derivedLocal = regexp.MustCompile(`let\s+(\w+)\s*:\s*string\[\]\s*=\s*\w+\.type_params`)

var fernFuncStart = regexp.MustCompile(`^(pub )?function\b`)

var funcDeclOpen = regexp.MustCompile(`\bFuncDecl\s*\{`)

func TestFuncDeclRebuildsKeepBoundTraits(t *testing.T) {
	for _, path := range selfHostSources {
		raw, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			t.Fatalf("read %s: %v", path, err)
		}
		lines := strings.Split(string(raw), "\n")
		for i, line := range lines {
			if strings.HasPrefix(strings.TrimSpace(line), "function ") {
				continue
			}
			loc := funcDeclOpen.FindStringIndex(line)
			if loc == nil {
				continue
			}
			lit, end := funcDeclLiteral(lines, i, loc[0])
			derived := readsDerivedLocal(lines, i, lit)
			if clearsTypeParams.MatchString(lit) && !derived && carriesBoundTraits.MatchString(lit) {
				t.Errorf("%s:%d-%d: this FuncDecl rebuild writes a fresh `type_params` but keeps "+
					"`bound_traits` from the source declaration, so the bounds are indexed against "+
					"type parameters the rebuild does not have.\n"+
					"Clear both — they are parallel arrays.\n%s",
					path, i+1, end+1, lit)
			}
			if !carriesTypeParams.MatchString(lit) && !derived {
				continue
			}
			if strings.Contains(lit, "bound_traits: []") {
				t.Errorf("%s:%d-%d: this FuncDecl rebuild carries `type_params` from an existing "+
					"declaration but clears `bound_traits`, so the decl still declares its type "+
					"parameters while claiming they are unbounded.\n"+
					"Use struct spread — `FuncDecl { ...<src>, body: … }` — which carries both and "+
					"cannot drop the next field added to FuncDecl either.\n%s",
					path, i+1, end+1, lit)
			}
		}
	}
}

func readsDerivedLocal(lines []string, i int, lit string) bool {
	m := typeParamsLocal.FindStringSubmatch(lit)
	if m == nil {
		return false
	}
	for j := i; j >= 0; j-- {
		if d := derivedLocal.FindStringSubmatch(lines[j]); d != nil && d[1] == m[1] {
			return true
		}
		if fernFuncStart.MatchString(lines[j]) {
			return false
		}
	}
	return false
}

func funcDeclLiteral(lines []string, i, col int) (string, int) {
	var b strings.Builder
	depth := 0
	started := false
	for j := i; j < len(lines) && j < i+24; j++ {
		text := lines[j]
		if j == i {
			text = text[col:]
		}
		b.WriteString(text)
		b.WriteString("\n")
		for _, ch := range text {
			switch ch {
			case '{':
				depth++
				started = true
			case '}':
				depth--
			}
		}
		if started && depth <= 0 {
			return b.String(), j
		}
	}
	return b.String(), i
}
