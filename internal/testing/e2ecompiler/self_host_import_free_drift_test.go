package e2ecompiler

import (
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

func TestImportFreeModulesDoNotDrift(t *testing.T) {
	paths, err := filepath.Glob(langSrcAbs(t, filepath.Join("compiler", "*.fern")))
	inDrivers, _ := filepath.Glob(langSrcAbs(t, filepath.Join("compiler", "drivers", "*.fern")))
	paths = append(paths, inDrivers...)
	if err != nil {
		t.Fatalf("glob self-host sources: %v", err)
	}
	if len(paths) == 0 {
		t.Fatal("no self-host sources found")
	}

	type copyOf struct {
		module string
		hash   string
		lines  int
	}
	bodies := map[string][]copyOf{}
	var free []string

	for _, p := range paths {
		src, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("read %s: %v", p, err)
		}
		lines := strings.Split(string(src), "\n")
		mod := strings.TrimSuffix(filepath.Base(p), ".fern")
		if !hasImport(lines) {
			free = append(free, mod)
		} else if mod != sharedCopies {
			continue
		}
		for name, body := range topLevelFuncs(lines) {
			code := normaliseCode(body)
			sum := md5.Sum([]byte(strings.Join(code, "\n")))
			bodies[name] = append(bodies[name], copyOf{mod, hex.EncodeToString(sum[:]), len(code)})
		}
	}

	sort.Strings(free)
	wantFree := []string{
		"aborts", "aesref", "arm64_native", "builtins", "capvocab", "docprintable", "elf", "literate", "rtbytecopy", "rtprofilemacho",
		"sourcepos", "ssadecrementcount", "ssafixed", "stdlib_methods", "typeinfo", "watbin", "wit_fern_world", "wit_proxy_world", "x86_native",
	}
	if strings.Join(free, ",") != strings.Join(wantFree, ",") {
		t.Errorf("the import-free module set changed.\n  got:  %v\n  want: %v\n"+
			"    These modules carry no `import` so e2ecompiler drivers can concatenate them with a\n"+
			"    driver main() — see x86_native.fern's header. A module that GAINED an import breaks\n"+
			"    those drivers; one that LOST its last import can join this list. Update wantFree and\n"+
			"    say in the commit message which driver you checked.", free, wantFree)
	}

	allowed := map[string]string{
		"main": "every driver entry point is different by definition.",
	}

	var names []string
	for n := range bodies {
		names = append(names, n)
	}
	sort.Strings(names)

	for _, name := range names {
		copies := bodies[name]
		if len(copies) < 2 {
			continue
		}
		distinct := map[string]bool{}
		for _, c := range copies {
			distinct[c.hash] = true
		}
		if len(distinct) == 1 {
			continue
		}
		if why, ok := allowed[name]; ok {
			t.Logf("%s: %d copies differ, allowed — %s", name, len(copies), why)
			continue
		}
		var detail []string
		for _, c := range copies {
			detail = append(detail, fmt.Sprintf("        %-18s %s  %d code lines", c.module, c.hash[:12], c.lines))
		}
		sort.Strings(detail)
		t.Errorf("%s has %d copies across import-free modules and they have DIVERGED (%d distinct bodies):\n%s\n"+
			"    These modules cannot import each other — e2ecompiler drivers concatenate them with a\n"+
			"    driver main(), which only works while they pull in nothing — so the duplication is\n"+
			"    deliberate and the copies must be kept in step BY HAND. A fix applied to one and not\n"+
			"    the others is silent: every driver still builds and the fixpoint still converges.\n"+
			"    Apply the change to every copy. If the copies are genuinely meant to differ, add the\n"+
			"    name to `allowed` above with the reason.",
			name, len(copies), len(distinct), strings.Join(detail, "\n"))
	}
}

const sharedCopies = "util"

var (
	importRe = regexp.MustCompile(`^import\s`)
	funcRe   = regexp.MustCompile(`^(?:pub )?function (\w+)`)
	lineCmt  = regexp.MustCompile(`\s*//.*$`)
)

func hasImport(lines []string) bool {
	for _, l := range lines {
		if importRe.MatchString(l) {
			return true
		}
	}
	return false
}

func topLevelFuncs(lines []string) map[string][]string {
	out := map[string][]string{}
	for i := 0; i < len(lines); i++ {
		m := funcRe.FindStringSubmatch(lines[i])
		if m == nil {
			continue
		}
		j := i
		for j < len(lines) && lines[j] != "}" {
			j++
		}
		if j < len(lines) {
			out[m[1]] = append([]string{}, lines[i:j+1]...)
		}
		i = j
	}
	return out
}

func normaliseCode(body []string) []string {
	var out []string
	for i, l := range body {
		if i == 0 {
			l = strings.Replace(l, "pub ", "", 1)
		}
		l = strings.TrimRight(lineCmt.ReplaceAllString(l, ""), " \t")
		if strings.TrimSpace(l) == "" {
			continue
		}
		out = append(out, l)
	}
	return out
}
