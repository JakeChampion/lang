// Command gen_selfhost_lists prints the Fern list literals
// compiler/asmcore.fern carries for the strerror table, so the
// self-host copy is regenerated from internal/tables/strerror rather than
// typed. Paste the output over the strerror_* bodies and
// wasi_error_code_errnos, and the constants over internal/stdlib/std/errno.fern's;
// selfhost_parity_test.go checks the result.
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
}
