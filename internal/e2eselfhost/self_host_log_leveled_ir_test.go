package e2eselfhost

import "testing"

// logLeveledIRCases exercise std/log's leveled `Logger` / `LogEntry` surface
// (#2683) through the self-host IR path on x86-64 + wasm — the `std/log` row
// was unaudited (⬜) for self-host. The two structs + their builder/render
// methods are inlined rather than imported; this verifies the constructs the
// leveled logger lowers to compile on the IR path: structs with i32 / boolean /
// string fields, struct-returning receiver methods chained
// (`lg.info_().str(...).int(...).bool(...)`), struct field reads, the
// threshold-filter branch, byte-indexed JSON escaping, and `i32.to_string()` +
// string concat. Each program returns the rendered line's length (kept <= 126).
// Expectations are hardcoded, verified against the reference interpreter.
// FEATURE-AUDIT std/log row.
const logLeveledIRPrelude = `struct Logger { min_level: i32, json: boolean }
struct LogEntry { min_level: i32, is_json: boolean, level: i32, text: string, json: string }
function level_trace(): i32 { return 0; }
function level_debug(): i32 { return 1; }
function level_info(): i32 { return 2; }
function level_warn(): i32 { return 3; }
function level_error(): i32 { return 4; }
function level_name(n: i32): string {
    if (n <= 0) { return "TRACE"; }
    if (n == 1) { return "DEBUG"; }
    if (n == 2) { return "INFO"; }
    if (n == 3) { return "WARN"; }
    return "ERROR";
}
function new_logger(min_level: i32): Logger {
    return Logger { min_level: min_level, json: false };
}
function new_json_logger(min_level: i32): Logger {
    return Logger { min_level: min_level, json: true };
}
function log_json_escape(s: string): string {
    var out: string = "";
    var i: i32 = 0;
    while (i < s.len()) {
        var c: i32 = s[i] as i32;
        if (c == 92) { out = out + "\\\\"; }
        else if (c == 34) { out = out + "\\\""; }
        else if (c == 10) { out = out + "\\n"; }
        else if (c == 13) { out = out + "\\r"; }
        else if (c == 9) { out = out + "\\t"; }
        else { out = out + slice_unchecked(s, i, i + 1); }
        i = i + 1;
    }
    return out;
}
function (lg: Logger) at(level: i32): LogEntry {
    return LogEntry { min_level: lg.min_level, is_json: lg.json, level: level, text: "", json: "" };
}
function (lg: Logger) trace_(): LogEntry { return lg.at(level_trace()); }
function (lg: Logger) debug_(): LogEntry { return lg.at(level_debug()); }
function (lg: Logger) info_(): LogEntry { return lg.at(level_info()); }
function (lg: Logger) warn_(): LogEntry { return lg.at(level_warn()); }
function (lg: Logger) error_(): LogEntry { return lg.at(level_error()); }
function (e: LogEntry) str(key: string, val: string): LogEntry {
    var t: string = e.text + " " + key + "=" + val;
    var j: string = e.json + ",\"" + log_json_escape(key) + "\":\"" + log_json_escape(val) + "\"";
    return LogEntry { min_level: e.min_level, is_json: e.is_json, level: e.level, text: t, json: j };
}
function (e: LogEntry) int(key: string, val: i32): LogEntry {
    var t: string = e.text + " " + key + "=" + val.to_string();
    var j: string = e.json + ",\"" + log_json_escape(key) + "\":" + val.to_string();
    return LogEntry { min_level: e.min_level, is_json: e.is_json, level: e.level, text: t, json: j };
}
function (e: LogEntry) bool(key: string, val: boolean): LogEntry {
    var vs: string = "false";
    if (val) { vs = "true"; }
    var t: string = e.text + " " + key + "=" + vs;
    var j: string = e.json + ",\"" + log_json_escape(key) + "\":" + vs;
    return LogEntry { min_level: e.min_level, is_json: e.is_json, level: e.level, text: t, json: j };
}
function (e: LogEntry) render(msg: string): string {
    if (e.level < e.min_level) { return ""; }
    if (e.is_json) {
        return "{\"level\":\"" + level_name(e.level) + "\",\"msg\":\"" + log_json_escape(msg) + "\"" + e.json + "}\n";
    }
    return "[" + level_name(e.level) + "] " + msg + e.text + "\n";
}
`

var logLeveledIRCases = []struct {
	name string
	main string
	want int
}{
	// plain text with chained str/int/bool fields above the threshold:
	// "[INFO] hi u=ann id=7 ok=true\n" -> 29.
	{"plain-fields", `var lg: Logger = new_logger(level_info()); return lg.info_().str("u", "ann").int("id", 7).bool("ok", true).render("hi").len();`, 29},
	// JSON-lines render of an error record with an int field:
	// `{"level":"ERROR","msg":"boom","code":42}\n` -> 41.
	{"json-record", `var lg: Logger = new_json_logger(level_warn()); return lg.error_().int("code", 42).render("boom").len();`, 41},
	// below-threshold record renders to "" (len 0) — the filter branch.
	{"filtered", `var lg: Logger = new_logger(level_info()); return lg.debug_().str("k", "v").render("noisy").len();`, 0},
	// JSON escaping of a quote + newline in the message:
	// `{"level":"TRACE","msg":"a\"b\nc"}\n` -> 34.
	{"json-escape", `var lg: Logger = new_json_logger(level_trace()); return lg.trace_().render("a\"b\nc").len();`, 34},
	// at-threshold boundary (warn == warn) emits; level_name picks WARN:
	// "[WARN] edge\n" -> 12.
	{"boundary", `var lg: Logger = new_logger(level_warn()); return lg.warn_().render("edge").len();`, 12},
}

func logLeveledIRSrc(mainBody string) string {
	return "import \"std/i32\";\nimport \"std/string\";\n" + logLeveledIRPrelude + "\nfunction main(): i32 { " + mainBody + " }\n"
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
