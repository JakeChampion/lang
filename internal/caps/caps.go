// Package caps hosts the builtin→capability inventory and the
// per-package capability report behind the capability-grant design
// (docs/PACKAGE-CAPABILITIES-BRIEF.md; tracking issue #5361).
//
// This is Phase 1 — inventory + report mode only. The table below
// tags every capability-relevant runtime builtin with the v1
// vocabulary (`net`, `fs`, `env`, `subprocess`, `time`, `random`);
// `fern -capabilities` computes each package's transitive reach into
// the table and prints it. Zero enforcement: no manifest key is read
// and no error is ever produced.
//
// The inventory's completeness contract: every user-callable builtin
// registered by the checker (Info.FuncSigs) or the interpreter
// (Interp.Builtins) must appear in exactly one of BuiltinCaps or
// Ungated, or carry the `__` compiler-internal prefix. The tests in
// this package enumerate both registries and fail when a new builtin
// lands unclassified, so an I/O-shaped addition can't silently dodge
// the table.
package caps

// Capabilities is the v1 capability vocabulary, sorted. Deliberately
// coarse (see the brief): path- or host-level filtering is out of
// scope for v1.
var Capabilities = []string{"env", "fs", "net", "random", "subprocess", "time"}

// BuiltinCaps maps each capability-relevant runtime builtin to its v1
// capability. Notes on the boundary cases:
//
//   - `poll` is NOT tagged: it waits on pollables from both sockets
//     (tcp_pollable) and timers (timer_fd / wasm_timer_pollable), so
//     the capability lives on the pollable *constructors* — the tcp
//     family under `net`, the timer family under `time` — not on the
//     wait itself. Same for the generic wasm_block / wasm_poll /
//     wasm_pollable_drop readiness helpers.
//   - `time` covers observing clocks (now_unix_ms / now_ns /
//     monotonic_ns) and creating time-driven wakeups (sleep_ms /
//     timer_fd / wasm_timer_pollable) — the surface a "package is
//     sim-pure" property would need to gate.
//   - stdio (print / eprint / read_line / stdin / stdout / stderr /
//     putchar / write) and argv (`args`) have no v1 capability; they
//     are host-mediated channels the invoker already handed the
//     process, not ambient authority a dependency escalates through.
var BuiltinCaps = map[string]string{
	"tcp_listen":   "net",
	"tcp_accept":   "net",
	"tcp_connect":  "net",
	"tcp_recv":     "net",
	"tcp_send":     "net",
	"tcp_close":    "net",
	"tcp_pollable": "net",
	"udp_send":     "net",

	"read_file":      "fs",
	"write_file":     "fs",
	"open_reader":    "fs",
	"open_writer":    "fs",
	"open_appender":  "fs",
	"stat":           "fs",
	"read_dir":       "fs",
	"remove_file":    "fs",
	"remove_dir_all": "fs",
	"temp_dir":       "fs",

	"env": "env",

	"subprocess":   "subprocess",
	"proc_fork":    "subprocess",
	"proc_waitpid": "subprocess",

	"now_unix_ms":         "time",
	"now_ns":              "time",
	"monotonic_ns":        "time",
	"sleep_ms":            "time",
	"timer_fd":            "time",
	"wasm_timer_pollable": "time",

	"random_bytes": "random",
	"random_i32":   "random",
}

// Ungated lists every user-callable builtin known to require NO
// capability: stdio, argv, process exit, pure math / bit casts, the
// strbuf scratch buffer, in-heap constructors (map_new / cell_new /
// string_from_bytes), the readiness helpers whose authority lives on
// the pollable constructors instead, and the interp's pure stdlib
// overrides. A builtin absent from both this set and BuiltinCaps
// fails the inventory-completeness tests.
var Ungated = map[string]bool{
	"putchar":            true,
	"print":              true,
	"write":              true,
	"eprint":             true,
	"read_line":          true,
	"stdin":              true,
	"stdout":             true,
	"stderr":             true,
	"args":               true,
	"exit":               true,
	"strbuf_reset":       true,
	"strbuf_append":      true,
	"strbuf_take":        true,
	"f32_bits":           true,
	"f32_from_bits":      true,
	"f64_bits":           true,
	"f64_from_bits":      true,
	"poll":               true,
	"wasm_block":         true,
	"wasm_poll":          true,
	"wasm_pollable_drop": true,
	"map_new":            true,
	"cell_new":           true,
	"string_from_bytes":  true,
	"int_to_string":      true,
}
