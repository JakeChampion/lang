package e2ecompiler

import "testing"

var logLeveledIRCases = []struct {
	name string
	main string
	want int
}{
	{"plain-fields", `let lg: log.Logger = log.new(log.INFO); return lg.info_().str("u", "ann").int("id", 7).bool("ok", true).render("hi").len();`, 29},
	{"json-record", `let lg: log.Logger = log.new_json(log.WARN); return lg.error_().int("code", 42).render("boom").len();`, 41},
	{"filtered", `let lg: log.Logger = log.new(log.INFO); return lg.debug_().str("k", "v").render("noisy").len();`, 0},
	{"json-escape", `let lg: log.Logger = log.new_json(log.TRACE); return lg.trace_().render("a\"b\nc").len();`, 34},
	{"boundary", `let lg: log.Logger = log.new(log.WARN); return lg.warn_().render("edge").len();`, 12},
}

func logLeveledIRSrc(mainBody string) string {
	return "import \"std/log\";\nfunction main(): i32 { " + mainBody + " }\n"
}

func TestSelfHostLogLeveledIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "wasm32-wasi"} {
		for _, tc := range logLeveledIRCases {
			t.Run(target+"/"+tc.name, func(t *testing.T) {
				if stderr, code := cli.exitOf(t, logLeveledIRSrc(tc.main), target); code != tc.want {
					t.Errorf("exited %d, want %d\n%s", code, tc.want, stderr)
				}
			})
		}
	}
}
