// Command gen_selfhost_lists prints the Fern list literals
// examples/self_host/asmcore.fern carries for the strerror table, so the
// self-host copy is regenerated from internal/strerror rather than
// typed. Paste the output over the four strerror_* bodies and
// wasi_error_code_errnos; selfhost_parity_test.go checks the result.
package main

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/jakechampion/lang/internal/strerror"
)

func main() {
	var texts, linux, darwin, wasi, codes []string
	for _, e := range strerror.Table {
		texts = append(texts, strconv.Quote(e.Text))
		linux = append(linux, strconv.Itoa(e.Linux))
		darwin = append(darwin, strconv.Itoa(e.Darwin))
		wasi = append(wasi, strconv.Itoa(e.Wasi))
	}
	for _, ec := range strerror.WasiErrorCodes {
		codes = append(codes, strconv.Itoa(strerror.Number(strerror.Wasi, ec.Errno)))
	}
	fmt.Printf("pub function strerror_texts(): string[] {\n    return [%s];\n}\n", strings.Join(texts, ", "))
	fmt.Printf("pub function strerror_linux(): i32[] {\n    return [%s];\n}\n", strings.Join(linux, ", "))
	fmt.Printf("pub function strerror_darwin(): i32[] {\n    return [%s];\n}\n", strings.Join(darwin, ", "))
	fmt.Printf("pub function strerror_wasi(): i32[] {\n    return [%s];\n}\n", strings.Join(wasi, ", "))
	fmt.Printf("pub function wasi_error_code_errnos(): i32[] {\n    return [%s];\n}\n", strings.Join(codes, ", "))
}
