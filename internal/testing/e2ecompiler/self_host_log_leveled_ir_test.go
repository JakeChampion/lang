package e2ecompiler

import "testing"

// The logging cases exercise the public module through the self-hosted compiler.
var logLeveledIRCases = []struct {
	name string
	main string
	want int
}{
	// plain text with chained str/int/bool fields above the threshold:
	// "[INFO] hi u=ann id=7 ok=true\n" -> 29.
	{"plain-fields", `let lg: log.Logger = log.new(log.INFO); return lg.info_().str("u", "ann").int("id", 7).bool("ok", true).render("hi").len();`, 29},
	// JSON-lines render of an error record with an int field:
	// `{"level":"ERROR","msg":"boom","code":42}\n` -> 41.
	{"json-record", `let lg: log.Logger = log.new_json(log.WARN); return lg.error_().int("code", 42).render("boom").len();`, 41},
	// below-threshold record renders to "" (len 0) — the filter branch.
	{"filtered", `let lg: log.Logger = log.new(log.INFO); return lg.debug_().str("k", "v").render("noisy").len();`, 0},
	// JSON escaping of a quote + newline in the message:
	// `{"level":"TRACE","msg":"a\"b\nc"}\n` -> 34.
	{"json-escape", `let lg: log.Logger = log.new_json(log.TRACE); return lg.trace_().render("a\"b\nc").len();`, 34},
	// at-threshold boundary (warn == warn) emits; level_name picks WARN:
	// "[WARN] edge\n" -> 12.
	{"boundary", `let lg: log.Logger = log.new(log.WARN); return lg.warn_().render("edge").len();`, 12},
}

func logLeveledIRSrc(mainBody string) string {
	return "import \"std/log\";\nfunction main(): i32 { " + mainBody + " }\n"
}

// TestSelfHostLogLeveledIR compiles each case with the self-host CLI for
// x86-64 and wasm and checks the exit code.
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
