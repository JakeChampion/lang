package e2ecompiler

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/pkg/modload"
	"github.com/jakechampion/lang/internal/syntax/printer"
)

// The stdin drivers need a complete program with no unresolved imports.
func assemblerSource(t *testing.T, name string) string {
	t.Helper()
	prog, _, err := modload.Load(langSrcAbs(t, "compiler/"+name))
	if err != nil {
		t.Fatalf("load %s: %v", name, err)
	}
	return printer.Format(prog)
}

// assemblerJoin flattens several compiler modules as one root module, so the
// drivers see each module's names bare and each shared import once.
func assemblerJoin(t *testing.T, names ...string) string {
	t.Helper()
	entry := langSrcAbs(t, "compiler/joined.fern")
	prog, _, err := modload.LoadWith(entry, map[string]string{entry: joinedSource(t, names...)})
	if err != nil {
		t.Fatalf("load %v: %v", names, err)
	}
	return printer.Format(prog)
}

// joinedSource is several compiler modules as one module, with each import once.
func joinedSource(t *testing.T, names ...string) string {
	t.Helper()
	seen := map[string]bool{}
	var imports, bodies strings.Builder
	for _, name := range names {
		src := string(mustRead(t, langSrcAbs(t, "compiler/"+name)))
		for _, line := range strings.Split(src, "\n") {
			if strings.HasPrefix(line, "import ") {
				if !seen[line] {
					seen[line] = true
					imports.WriteString(line + "\n")
				}
				continue
			}
			bodies.WriteString(line + "\n")
		}
	}
	return imports.String() + bodies.String()
}
