package ciplan

import (
	"path"
	"regexp"
	"strings"
)

var importPattern = regexp.MustCompile(`(?m)^\s*(?:pub\s+)?(?:import|use)\s+"([^"]+)"`)
var referencePattern = regexp.MustCompile(`(?:compiler|coreutils|diffutils|examples|tests|bench|fern-image|internal|spec|conformance|scripts|site|web|editors|bootstrap|docs|\.github)/[A-Za-z0-9_./*?\[\]-]*`)

func (g *Graph) references(value string, out map[string]bool) {
	simple := value
	for strings.HasPrefix(simple, "../") {
		simple = strings.TrimPrefix(simple, "../")
	}
	simple = strings.TrimPrefix(simple, "./")
	simple = strings.TrimPrefix(simple, g.Root+"/")
	for _, match := range referencePattern.FindAllString(value, -1) {
		p := path.Clean(match)
		// A /docs/ URL or a temporary docs/ directory is not the repo root.
		// Explicit relative paths and concrete files keep their dependencies.
		if p == "docs" && !strings.HasPrefix(value, "../") && !strings.HasPrefix(value, g.Root+"/") {
			continue
		}
		if strings.HasSuffix(match, "/") || path.Ext(p) == "" {
			out[p] = true
			out[p+"/**"] = true
		} else {
			out[p] = true
		}
	}
	if _, ok := g.Config.Components[simple]; ok {
		out[simple+"/**"] = true
	}
	for _, root := range []string{"compiler", "coreutils", "diffutils", "examples", "tests", "bench", "fern-image", "internal", "spec", "conformance", "scripts", "site", "web", "editors", "bootstrap", ".github"} {
		if simple == root {
			out[root] = true
			out[root+"/**"] = true
		}
	}
	if value == "stdlib" {
		out["internal/stdlib/**"] = true
	}
	for _, dep := range g.imports("", value) {
		out[dep] = true
	}
	if strings.HasSuffix(value, ".fern") && !strings.ContainsAny(value, "\n\t ") {
		p := path.Join("compiler", value)
		if g.files[p] {
			out[p] = true
		}
	}
}

func (g *Graph) imports(file, source string) []string {
	var out []string
	for _, match := range importPattern.FindAllStringSubmatch(source, -1) {
		name := match[1]
		var p string
		switch {
		case strings.HasPrefix(name, "std/"), strings.HasPrefix(name, "core/"):
			p = path.Join("internal/stdlib", name)
		case file != "" && strings.HasPrefix(name, "."):
			p = path.Join(path.Dir(file), name)
		default:
			continue
		}
		// Keep both candidates so removals and extension changes retain their consumers.
		out = append(out, p+".fern", p+".fern.md")
	}
	return out
}

func (g *Graph) inputHit(inputs map[string]bool, changes []string) bool {
	seen := map[string]bool{}
	var hit func(string) bool
	hit = func(input string) bool {
		if seen[input] {
			return false
		}
		seen[input] = true
		matched := MatchesAny([]string{input}, changes)
		if strings.HasSuffix(input, "/@production") || strings.HasSuffix(input, "/@tests") {
			dir := path.Dir(input)
			for _, file := range changes {
				if path.Dir(file) == dir && goSource(file) {
					matched = matched || !strings.HasSuffix(file, "_test.go") || strings.HasSuffix(input, "/@tests")
				} else if strings.HasPrefix(file, dir+"/") && !goSource(file) {
					matched = true
				}
			}
		}
		if matched {
			return true
		}
		for _, dep := range g.fern[input] {
			if hit(dep) {
				return true
			}
		}
		return false
	}
	for input := range inputs {
		if hit(input) {
			return true
		}
	}
	return false
}

// sourceChanges includes programs that import a changed Fern module. Directory
// consumers then see dependencies outside the directory they scan.
func (g *Graph) sourceChanges(changes []string) []string {
	out := append([]string{}, changes...)
	seen := map[string]bool{}
	for _, file := range out {
		seen[file] = true
	}
	for i := 0; i < len(out); i++ {
		for _, importer := range g.fernReverse[out[i]] {
			if !seen[importer] {
				seen[importer] = true
				out = append(out, importer)
			}
		}
	}
	return out
}
