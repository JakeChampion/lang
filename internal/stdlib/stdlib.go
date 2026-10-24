package stdlib

import (
	"embed"
	"io/fs"
	"strings"
)

//go:embed all:std all:core
var src embed.FS

func Resolve(importPath string) (string, bool) {
	if !IsStdlibPath(importPath) {
		return "", false
	}
	data, err := fs.ReadFile(src, withFernExt(importPath))
	if err != nil {
		return "", false
	}
	return string(data), true
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
	if strings.HasSuffix(importPath, ".fern") {
		return importPath
	}
	return importPath + ".fern"
}

func FS() fs.FS { return src }
