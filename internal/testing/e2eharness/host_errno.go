package e2eharness

import (
	"fmt"
	"strings"

	"github.com/jakechampion/lang/internal/tables/strerror"
)

// HostErrnoFern is a Fern `host_errno(linux)`: the errno Linux numbers
// `linux`, as the compile target numbers it, which is what a socket builtin
// returns. It is generated from internal/tables/strerror, so a probe can fake
// a builtin's answer, or round-trip `NetError.errno()` through
// `net.error_from_host`, without copying a table of its own; an errno the
// target has no number for answers 0.
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
