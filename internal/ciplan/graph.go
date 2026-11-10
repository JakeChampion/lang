package ciplan

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

func ReadConfig(root string) (Config, error) {
	var config Config
	data, err := os.ReadFile(filepath.Join(root, ".github/ci/targets.json"))
	if err != nil {
		return config, err
	}
	if !json.Valid(data) {
		return config, fmt.Errorf("CI graph is not valid JSON")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&config); err != nil {
		return config, err
	}
	if config.Version != 1 || len(config.Targets) == 0 {
		return config, fmt.Errorf("unsupported or empty CI graph")
	}
	return config, nil
}

func Load(root string) (*Graph, error) {
	g := &Graph{Root: root, files: map[string]bool{}, text: map[string]string{}, packages: map[string]*goPackage{}, decls: map[string]*declaration{}, fern: map[string][]string{}}
	var err error
	g.Config, err = ReadConfig(root)
	if err != nil {
		return nil, err
	}
	module, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return nil, err
	}
	match := regexp.MustCompile(`(?m)^module\s+([^\s]+)`).FindSubmatch(module)
	if len(match) != 2 {
		return nil, fmt.Errorf("go.mod has no module directive")
	}
	g.module = strings.Trim(string(match[1]), `"`)
	gitRoot, gitErr := exec.Command("git", "-C", root, "rev-parse", "--show-toplevel").Output()
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	g.Root = absolute
	var paths []byte
	if gitErr == nil && strings.TrimSpace(string(gitRoot)) == absolute {
		paths, gitErr = exec.Command("git", "-C", root, "ls-files", "-z", "--cached", "--others", "--exclude-standard").Output()
	} else {
		gitErr = fmt.Errorf("the module is not a Git root")
	}
	var files []string
	if gitErr == nil {
		for _, file := range strings.Split(string(paths), "\x00") {
			if file != "" {
				files = append(files, file)
			}
		}
	} else {
		err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			rel, err := filepath.Rel(root, p)
			if err != nil {
				return err
			}
			rel = filepath.ToSlash(rel)
			if d.IsDir() {
				if rel != "." && (slices.Contains([]string{".git", "node_modules", "worktrees"}, d.Name()) || slices.Contains([]string{"build", "bin", "target"}, rel) || strings.HasPrefix(rel, ".claude/worktrees/")) {
					return filepath.SkipDir
				}
				return nil
			}
			files = append(files, rel)
			return nil
		})
	}
	if err != nil {
		return nil, err
	}
	for _, file := range files {
		if info, err := os.Lstat(filepath.Join(root, filepath.FromSlash(file))); os.IsNotExist(err) || err == nil && info.IsDir() {
			continue
		} else if err != nil {
			return nil, err
		}
		g.files[file] = true
		if strings.HasSuffix(file, ".go") || strings.HasSuffix(file, ".fern") || strings.HasSuffix(file, ".fern.md") {
			data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(file)))
			if err != nil {
				return nil, err
			}
			g.text[file] = string(data)
		}
	}
	for name, component := range g.Config.Components {
		for _, dep := range component.Depends {
			if _, ok := g.Config.Components[dep]; !ok {
				return nil, fmt.Errorf("component %s names unknown dependency %s", name, dep)
			}
		}
	}
	state := map[string]int{}
	var visit func(string) error
	visit = func(name string) error {
		if state[name] == 1 {
			return fmt.Errorf("component dependency cycle at %s", name)
		}
		if state[name] == 2 {
			return nil
		}
		state[name] = 1
		for _, dep := range g.Config.Components[name].Depends {
			if err := visit(dep); err != nil {
				return err
			}
		}
		state[name] = 2
		return nil
	}
	for name := range g.Config.Components {
		if err := visit(name); err != nil {
			return nil, err
		}
	}
	if err := g.readGo(); err != nil {
		return nil, err
	}
	for lane, target := range g.Config.Targets {
		for _, name := range target.Components {
			if _, ok := g.Config.Components[name]; !ok {
				return nil, fmt.Errorf("target %s names unknown component %s", lane, name)
			}
		}
		for _, name := range target.Go {
			if name != "@units" && len(g.goRoots(name)) == 0 {
				return nil, fmt.Errorf("target %s names missing Go package %s", lane, name)
			}
		}
	}
	g.reverse = map[string][]string{}
	for name, decl := range g.decls {
		for ref := range decl.refs {
			g.reverse[ref] = append(g.reverse[ref], name)
		}
	}
	g.fernReverse = map[string][]string{}
	for file, source := range g.text {
		if strings.HasSuffix(file, ".fern") || strings.HasSuffix(file, ".fern.md") {
			g.fern[file] = g.imports(file, source)
			for _, dep := range g.fern[file] {
				g.fernReverse[dep] = append(g.fernReverse[dep], file)
			}
		}
	}
	for pkg, tests := range g.Config.OpaqueTests {
		for test, inputs := range tests {
			decl := g.decls[pkg+"."+test]
			if decl == nil {
				return nil, fmt.Errorf("opaque input declaration names missing test %s.%s", pkg, test)
			}
			for _, input := range inputs {
				decl.inputs[input] = true
			}
		}
	}
	g.special = map[string]string{}
	for lane := range g.Config.Targets {
		workflow, _ := os.ReadFile(filepath.Join(root, ".github/workflows/"+lane+".yml"))
		for _, line := range strings.Split(string(workflow), "\n") {
			if !strings.Contains(line, "ISOLATED_DRIVER_TESTS:") && !strings.Contains(line, "OWN_JOB_TESTS:") {
				continue
			}
			for _, name := range regexp.MustCompile(`\bTest[A-Za-z0-9_]+`).FindAllString(line, -1) {
				if name == "TestSelfHostGatherConcurrency" {
					continue
				}
				if owner := g.special[name]; owner != "" && owner != lane {
					return nil, fmt.Errorf("test %s has owners %s and %s", name, owner, lane)
				}
				g.special[name] = lane
			}
		}
	}

	return g, nil
}
