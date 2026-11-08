package ciplan

import (
	"os"
	"path"
	"path/filepath"
	"strings"
)

func (g *Graph) usedByWorkflow(file string) bool {
	for lane := range g.Config.Targets {
		if g.workflowHit(lane, []string{file}) {
			return true
		}
	}
	return false
}

func (g *Graph) workflowHit(lane string, changes []string) bool {
	seen := map[string]bool{}
	var visit func(string) bool
	visit = func(file string) bool {
		if seen[file] {
			return false
		}
		seen[file] = true
		data, err := os.ReadFile(filepath.Join(g.Root, filepath.FromSlash(file)))
		if err != nil {
			return false
		}
		var lines []string
		descriptionIndent := -1
		for _, line := range strings.Split(string(data), "\n") {
			trimmed := strings.TrimSpace(line)
			indent := len(line) - len(strings.TrimLeft(line, " "))
			if strings.HasPrefix(trimmed, "#") || trimmed == "" {
				continue
			}
			if descriptionIndent >= 0 && indent > descriptionIndent {
				continue
			}
			descriptionIndent = -1
			if strings.HasPrefix(trimmed, "description:") {
				descriptionIndent = indent
				continue
			}
			lines = append(lines, line)
		}
		source := strings.Join(lines, "\n")
		for _, changed := range changes {
			if strings.HasSuffix(changed, ".md") && !strings.HasSuffix(changed, ".fern.md") {
				continue
			}
			if changed == file || strings.Contains(source, changed) || strings.HasPrefix(changed, ".github/actions/") && strings.Contains(source, path.Dir(changed)) || strings.HasPrefix(changed, "cmd/") && strings.Contains(source, path.Dir(changed)) {
				return true
			}
		}
		for _, ref := range referencePattern.FindAllString(source, -1) {
			ref = strings.TrimSuffix(ref, "/")
			if !strings.HasPrefix(ref, "scripts/") && !strings.HasPrefix(ref, ".github/") {
				continue
			}
			if strings.HasPrefix(ref, ".github/actions/") {
				ref = path.Join(ref, "action.yml")
			}
			if g.files[ref] && visit(ref) {
				return true
			}
		}
		return false
	}
	return visit(".github/workflows/" + lane + ".yml")
}
