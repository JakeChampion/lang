package e2eharness

import (
	"fmt"
	"strings"

	"github.com/jakechampion/lang/internal/tables/strerror"
)

func HostErrnoFern() string {
	var b strings.Builder
	b.WriteString("function host_errno(linux: i32): i32 {\n    let os: string = target_os();\n")
	for _, t := range []struct{ os, cond string }{
		{strerror.Darwin, `os == "darwin"`},
		{strerror.Wasi, `os == "wasi" || os == "wasi-http"`},
	} {
		fmt.Fprintf(&b, "    if (%s) {\n", t.cond)
		for _, e := range strerror.Table {
			if e.Linux != 0 {
				fmt.Fprintf(&b, "        if (linux == %d) { return %d; }\n", e.Linux, e.Number(t.os))
			}
		}
		b.WriteString("        return 0;\n    }\n")
	}
	b.WriteString("    return linux;\n}\n")
	return b.String()
}
