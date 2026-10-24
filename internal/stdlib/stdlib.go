package stdlib

import (
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"slices"
	"strings"

	"github.com/jakechampion/lang/internal/tools/literate"
)

//go:embed all:std all:core
var src embed.FS

func Resolve(importPath string) (string, bool) {
	m, err := ReadModule(importPath)
	return m.Code, err == nil
}

type ModuleSource struct {
	Code      string
	DocPath   string
	DocSource string
	LineMap   []literate.Line
}

func ReadModule(importPath string) (ModuleSource, error) {
	if !IsStdlibPath(importPath) {
		return ModuleSource{}, &fs.PathError{Op: "read", Path: importPath, Err: fs.ErrNotExist}
	}
	return readModule(src, withFernExt(importPath))
}

func readModule(files fs.FS, path string) (ModuleSource, error) {
	data, err := fs.ReadFile(files, path)
	if err == nil {
		return ModuleSource{Code: string(data)}, nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return ModuleSource{}, err
	}
	docPath := path + ".md"
	data, err = fs.ReadFile(files, docPath)
	if err != nil {
		return ModuleSource{}, err
	}
	doc := literate.Parse(string(data))
	if doc.HasFiles() {
		return ModuleSource{}, fmt.Errorf("%s: a standard library document must define one module", docPath)
	}
	code, lines, err := doc.Tangle()
	if err != nil {
		return ModuleSource{}, fmt.Errorf("%s: %w", docPath, err)
	}
	return ModuleSource{Code: code, DocPath: docPath, DocSource: string(data), LineMap: lines}, nil
}

func ModuleName(path string) string {
	if strings.HasSuffix(path, ".fern.md") {
		return strings.TrimSuffix(path, ".fern.md")
	}
	if strings.HasSuffix(path, ".fern") {
		return strings.TrimSuffix(path, ".fern")
	}
	return ""
}

func Modules() ([]string, error) {
	names := map[string]bool{}
	err := fs.WalkDir(src, ".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			if name := ModuleName(path); IsStdlibPath(name) {
				names[name] = true
			}
		}
		return nil
	})
	var modules []string
	for name := range names {
		modules = append(modules, name)
	}
	slices.Sort(modules)
	return modules, err
}

func IsStdlibPath(importPath string) bool {
	return strings.HasPrefix(importPath, "std/") || strings.HasPrefix(importPath, "core/")
}

func ModuleKey(importPath string) string {
	if !IsStdlibPath(importPath) {
		return ""
	}
	return "stdlib://" + withFernExt(importPath)
}

func withFernExt(importPath string) string {
	if name := ModuleName(importPath); name != "" {
		return name + ".fern"
	}
	return importPath + ".fern"
}

func FS() fs.FS { return src }
