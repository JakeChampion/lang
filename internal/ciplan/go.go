package ciplan

import (
	"fmt"
	"go/ast"
	"go/build"
	"go/parser"
	"go/token"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
)

func (g *Graph) readGo() error {
	parsedFiles := map[string]*ast.File{}
	packageNames := map[string]string{}
	receivers := map[string][]string{}
	for file, source := range g.text {
		if !goSource(file) {
			continue
		}
		parsed, err := parser.ParseFile(token.NewFileSet(), file, source, parser.SkipObjectResolution)
		if err != nil {
			return fmt.Errorf("parse %s: %w", file, err)
		}
		parsedFiles[file] = parsed
		if !strings.HasSuffix(parsed.Name.Name, "_test") {
			packageNames[path.Dir(file)] = parsed.Name.Name
		}
	}
	for file, parsed := range parsedFiles {
		dir := path.Dir(file)
		pkg := g.packages[dir]
		if pkg == nil {
			pkg = &goPackage{imports: map[string]bool{}, testImports: map[string]bool{}, tests: map[string][]string{}, decls: map[string]*declaration{}, testOnly: map[string]bool{}}
			g.packages[dir] = pkg
		}
		pkg.files = append(pkg.files, file)
		isTest := strings.HasSuffix(file, "_test.go")
		pkg.testOnly[file] = isTest
		aliases := map[string]string{}
		pathAliases := map[string]bool{}
		for _, imp := range parsed.Imports {
			name, err := strconv.Unquote(imp.Path.Value)
			if err != nil {
				return err
			}
			local := strings.TrimPrefix(name, g.module+"/")
			alias := packageNames[local]
			if alias == "" {
				alias = path.Base(local)
			}
			if imp.Name != nil {
				alias = imp.Name.Name
			}
			if name == "path" || name == "path/filepath" {
				pathAliases[alias] = true
			}
			if !strings.HasPrefix(name, g.module+"/") {
				continue
			}
			aliases[alias] = local
			if isTest {
				pkg.testImports[local] = true
			} else {
				pkg.imports[local] = true
			}
		}
		for _, node := range parsed.Decls {
			var names []string
			switch n := node.(type) {
			case *ast.FuncDecl:
				names = []string{n.Name.Name}
				if n.Recv != nil && len(n.Recv.List) > 0 {
					if receiver := receiverName(n.Recv.List[0].Type); receiver != "" {
						receivers[dir+"."+receiver] = append(receivers[dir+"."+receiver], dir+"."+n.Name.Name)
					}
				}
				if isTest && strings.HasPrefix(n.Name.Name, "Test") && n.Name.Name != "TestMain" && n.Recv == nil {
					pkg.tests[n.Name.Name] = append(pkg.tests[n.Name.Name], file)
				} else if isTest {
					pkg.testOnly[file] = false
				}
			case *ast.GenDecl:
				if n.Tok == token.IMPORT {
					continue
				}
				if isTest {
					pkg.testOnly[file] = false
				}
				for _, spec := range n.Specs {
					switch s := spec.(type) {
					case *ast.ValueSpec:
						for _, name := range s.Names {
							names = append(names, name.Name)
						}
					case *ast.TypeSpec:
						names = append(names, s.Name.Name)
					}
				}
			}
			decl := &declaration{inputs: map[string]bool{}, refs: map[string]bool{}}
			joined := map[*ast.BasicLit]bool{}
			ast.Inspect(node, func(n ast.Node) bool {
				switch n := n.(type) {
				case *ast.CallExpr:
					join := false
					switch fn := n.Fun.(type) {
					case *ast.SelectorExpr:
						if alias, ok := fn.X.(*ast.Ident); ok {
							join = fn.Sel.Name == "Join" && pathAliases[alias.Name] || fn.Sel.Name == "RepoPath"
						}
					case *ast.Ident:
						join = fn.Name == "RepoPath"
					}
					if join {
						var parts []string
						flush := func() {
							if len(parts) > 0 {
								g.references(path.Join(parts...), decl.inputs)
								parts = nil
							}
						}
						for _, arg := range n.Args {
							if literal, ok := arg.(*ast.BasicLit); ok && literal.Kind == token.STRING {
								value, err := strconv.Unquote(literal.Value)
								if err == nil {
									joined[literal] = true
									parts = append(parts, value)
								}
							} else {
								flush()
							}
						}
						flush()
					}
				case *ast.Ident:
					decl.refs[dir+"."+n.Name] = true
				case *ast.SelectorExpr:
					if id, ok := n.X.(*ast.Ident); ok && aliases[id.Name] != "" {
						decl.refs[aliases[id.Name]+"."+n.Sel.Name] = true
					}
				case *ast.BasicLit:
					if n.Kind == token.STRING && !joined[n] {
						value, err := strconv.Unquote(n.Value)
						if err == nil {
							g.references(value, decl.inputs)
						}
					}
				}
				return true
			})
			for _, name := range names {
				key := dir + "." + name
				if old := g.decls[key]; old != nil {
					for input := range old.inputs {
						decl.inputs[input] = true
					}
					for ref := range old.refs {
						decl.refs[ref] = true
					}
				}
				g.decls[key] = decl
				pkg.decls[name] = decl
			}
		}
	}
	for receiver, methods := range receivers {
		if decl := g.decls[receiver]; decl != nil {
			for _, method := range methods {
				decl.refs[method] = true
			}
		}
	}
	return nil
}

func receiverName(expr ast.Expr) string {
	switch expr := expr.(type) {
	case *ast.Ident:
		return expr.Name
	case *ast.StarExpr:
		return receiverName(expr.X)
	case *ast.IndexExpr:
		return receiverName(expr.X)
	case *ast.IndexListExpr:
		return receiverName(expr.X)
	}
	return ""
}

func (g *Graph) packageFiles(name string, tests bool, seen map[string]bool) map[string]bool {
	out := map[string]bool{}
	if seen[name] {
		return out
	}
	seen[name] = true
	pkg := g.packages[name]
	if pkg == nil {
		return out
	}
	for _, file := range pkg.files {
		if tests || !strings.HasSuffix(file, "_test.go") {
			out[file] = true
		}
	}
	out[name+"/@production"] = true
	if tests {
		out[name+"/@tests"] = true
	}
	imports := map[string]bool{}
	for name := range pkg.imports {
		imports[name] = true
	}
	if tests {
		for name := range pkg.testImports {
			imports[name] = true
		}
	}
	for name := range imports {
		for file := range g.packageFiles(name, false, seen) {
			out[file] = true
		}
	}
	return out
}

func (g *Graph) goRoots(name string) []string {
	var roots []string
	if strings.HasSuffix(name, "/...") {
		prefix := strings.TrimSuffix(name, "/...")
		for pkg := range g.packages {
			if pkg == prefix || strings.HasPrefix(pkg, prefix+"/") {
				roots = append(roots, pkg)
			}
		}
	} else if g.packages[name] != nil {
		roots = append(roots, name)
	}
	sort.Strings(roots)
	return roots
}

func (g *Graph) affectedDeclarations(changes []string) map[string]bool {
	hits := map[string]bool{}
	var queue []string
	for pkg, data := range g.packages {
		for test, files := range data.tests {
			if intersects(changes, files) {
				key := pkg + "." + test
				hits[key] = true
				queue = append(queue, key)
			}
		}
	}
	for name, decl := range g.decls {
		if g.inputHit(decl.inputs, changes) {
			hits[name] = true
			queue = append(queue, name)
		}
	}
	for len(queue) > 0 {
		name := queue[0]
		queue = queue[1:]
		for _, caller := range g.reverse[name] {
			if !hits[caller] {
				hits[caller] = true
				queue = append(queue, caller)
			}
		}
	}
	return hits
}

func (g *Graph) affectedTests(pkg string, changes []string, hits map[string]bool) []string {
	p := g.packages[pkg]
	if p == nil {
		return nil
	}
	var tests []string
	for name, files := range p.tests {
		if intersects(changes, files) || hits[pkg+"."+name] {
			tests = append(tests, name)
		}
	}
	sort.Strings(tests)
	return tests
}

func intersects(a, b []string) bool {
	for _, value := range a {
		if slices.Contains(b, value) {
			return true
		}
	}
	return false
}

func (g *Graph) testPlatforms(pkg, name string) (linux, darwinOnly bool) {
	for _, file := range g.packages[pkg].tests[name] {
		value, ok := g.platform.Load(file)
		if !ok {
			var supported [3]bool
			for i, target := range [][2]string{{"linux", "amd64"}, {"linux", "arm64"}, {"darwin", "arm64"}} {
				context := build.Default
				context.GOOS, context.GOARCH, context.CgoEnabled = target[0], target[1], true
				context.ToolTags = nil
				for _, tag := range build.Default.ToolTags {
					if !strings.HasPrefix(tag, "amd64.") && !strings.HasPrefix(tag, "arm64.") {
						context.ToolTags = append(context.ToolTags, tag)
					}
				}
				if target[1] == "amd64" {
					context.ToolTags = append(context.ToolTags, "amd64.v1")
				} else {
					context.ToolTags = append(context.ToolTags, "arm64.v8.0")
				}
				supported[i], _ = context.MatchFile(filepath.Join(g.Root, filepath.FromSlash(path.Dir(file))), path.Base(file))
			}
			value = supported
			g.platform.Store(file, value)
		}
		supported := value.([3]bool)
		linux = linux || supported[0] || supported[1]
		darwinOnly = darwinOnly || supported[2] && !supported[0] && !supported[1]
	}
	return linux, darwinOnly
}

func unitPackage(name string) bool {
	for _, slow := range []string{"internal/testing/e2e", "internal/testing/e2ecompiler", "internal/testing/coreutils", "internal/testing/fernsmith"} {
		if name == slow || slow == "internal/testing/fernsmith" && strings.HasPrefix(name, slow+"/") {
			return false
		}
	}
	return true
}

func goSource(file string) bool {
	return strings.HasSuffix(file, ".go") && !strings.Contains(file, "/testdata/")
}
