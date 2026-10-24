package main

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/jakechampion/lang/internal/tables/strerror"
)

func main() {
	var texts, darwinTexts, linux, darwin, wasi, carried, consts, codes []string
	for _, e := range strerror.Table {
		texts = append(texts, strconv.Quote(e.Text))
		darwinTexts = append(darwinTexts, strconv.Quote(e.TextFor(strerror.Darwin)))
		linux = append(linux, strconv.Itoa(e.Linux))
		darwin = append(darwin, strconv.Itoa(e.Darwin))
		wasi = append(wasi, strconv.Itoa(e.Wasi))
		carried = append(carried, strconv.Itoa(e.Carried()))
		consts = append(consts, fmt.Sprintf("pub const %s: i32 = %d;", e.Name, e.Carried()))
	}
	for _, ec := range strerror.WasiErrorCodes {
		codes = append(codes, strconv.Itoa(strerror.Number(strerror.Wasi, ec.Errno)))
	}
	fmt.Printf("pub function strerror_texts(): string[] {\n    return [%s];\n}\n", strings.Join(texts, ", "))
	fmt.Printf("pub function strerror_texts_darwin(): string[] {\n    return [%s];\n}\n", strings.Join(darwinTexts, ", "))
	fmt.Printf("pub function strerror_linux(): i32[] {\n    return [%s];\n}\n", strings.Join(linux, ", "))
	fmt.Printf("pub function strerror_darwin(): i32[] {\n    return [%s];\n}\n", strings.Join(darwin, ", "))
	fmt.Printf("pub function strerror_wasi(): i32[] {\n    return [%s];\n}\n", strings.Join(wasi, ", "))
	fmt.Printf("pub function strerror_carried(): i32[] {\n    return [%s];\n}\n", strings.Join(carried, ", "))
	fmt.Printf("pub function wasi_error_code_errnos(): i32[] {\n    return [%s];\n}\n", strings.Join(codes, ", "))
	fmt.Printf("\n// internal/stdlib/std/errno.fern\n%s\n", strings.Join(consts, "\n\n"))
	fmt.Printf("\nfunction __darwin_errnos(): i32[] {\n  return [%s];\n}\n", strings.Join(darwin, ", "))
	fmt.Printf("\nfunction __wasi_errnos(): i32[] {\n  return [%s];\n}\n", strings.Join(wasi, ", "))
	fmt.Printf("\nfunction __carried_errnos(): i32[] {\n  return [%s];\n}\n", strings.Join(carried, ", "))
}
