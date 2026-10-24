package stdlib

import (
	"embed"
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

type ModuleSource = literate.Source

func ReadModule(importPath string) (ModuleSource, error) {
	if !IsStdlibPath(importPath) {
		return ModuleSource{}, &fs.PathError{Op: "read", Path: importPath, Err: fs.ErrNotExist}
	}
	return readModule(src, withFernExt(importPath))
}

func readModule(files fs.FS, path string) (ModuleSource, error) {
	return literate.ReadModule(files, path)
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
