// Package caps hosts the builtin→capability inventory and the
// per-package capability report behind the capability-grant design
// (docs/PACKAGE-CAPABILITIES-BRIEF.md; tracking issue #5361).
//
// The table below tags every capability-relevant runtime builtin with
// the v1 vocabulary (`net`, `fs`, `env`, `subprocess`, `time`,
// `random`). Analyze (report.go) computes each package's transitive
// reach into the table — `fern -capabilities` prints it (phase 1), and
// Enforce (enforce.go) checks the same rows against the manifests'
// `capabilities` grants (phase 2): a governed package reaching outside
// its grant is an E070 error, an ungoverned one a warn-and-allow
// diagnostic, and the root package is exempt.
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

	"read_file":       "fs",
	"read_file_bytes": "fs",
	"write_file":      "fs",
	"write_file_exec": "fs",
	"open_reader":     "fs",
	"open_writer":     "fs",
	"open_appender":   "fs",
	"open_exclusive":  "fs",
	"stat":            "fs",
	"getcwd":          "fs",
	"lstat":           "fs",
	"access":          "fs",
	"read_dir":        "fs",
	"remove_file":     "fs",
	"remove_dir_all":  "fs",
	"create_dir_all":  "fs",
	"create_dir":      "fs",
	"remove_dir":      "fs",
	"create_link":     "fs",
	"create_symlink":  "fs",
	"read_link":       "fs",
	"rename":          "fs",
	"chmod":           "fs",
	"set_file_times":  "fs",
	"truncate":        "fs",
	// Creating a FIFO or a device node creates a filesystem entry, so the
	// package question has none of the target question's subtlety here:
	// it is filesystem reach.
	"mknod": "fs",
	// Setting an entry's owner or group rewrites who may reach it, and
	// the entry is a filesystem one. For the PACKAGE question the only
	// thing that matters is whether filesystem reach should be visible,
	// so this sits with `chmod` rather than getting a split of its own.
	"chown_at": "fs",
	// The process file-mode creation mask. Reading it is ambient
	// information about the process, but SETTING it changes the mode of
	// every file and directory anything creates afterwards — a
	// dependency that reaches it reaches every later creation, so it
	// sits with the rest of the filesystem surface rather than beside
	// `geteuid` in Ungated.
	"umask": "fs",
	// Reading the geometry of a filesystem is filesystem reach; the
	// package question has none of the target question's subtlety here.
	"statfs":   "fs",
	"temp_dir": "fs",

	"env":     "env",
	"environ": "env",
	// The machine's name is ambient information about where the
	// process runs, in the same way its environment is: a dependency
	// that reads it should be seen to.
	"hostname": "env",
	// The kernel's utsname record and the machine's usable processor
	// count are the same kind of ambient fact about where the process
	// runs, and are seen to the same way.
	"uname_field": "env",
	"cpu_count":   "env",

	"subprocess":   "subprocess",
	"proc_fork":    "subprocess",
	"proc_waitpid": "subprocess",
	"proc_exec":    "subprocess",
	// Not spawning, but reaching outside this process all the same: the
	// pid asked about is someone else's.
	"process_alive": "subprocess",

	"now_unix_ms":         "time",
	"now_ns":              "time",
	"monotonic_ns":        "time",
	"sleep_ms":            "time",
	"sleep_ns":            "time",
	"timer_fd":            "time",
	"wasm_timer_pollable": "time",

	"random_bytes": "random",
	"random_i32":   "random",
}

// Ungated lists every user-callable builtin known to require NO
// capability: stdio (including `isatty`, which reads one bit about a
// stream the invoker already handed the process), argv, process exit,
// the compile target's name (`target_os`, a literal once compiled),
// pure math / bit casts, the
// strbuf scratch buffer, in-heap constructors (map_new / cell_new /
// string_from_bytes_unchecked), the readiness helpers whose authority lives on
// the pollable constructors instead, and the interp's pure stdlib
// overrides. A builtin absent from both this set and BuiltinCaps
// fails the inventory-completeness tests.
var Ungated = map[string]bool{
	"putchar":   true,
	"print":     true,
	"write":     true,
	"eprint":    true,
	"read_line": true,
	"stdin":     true,
	"stdout":    true,
	"stderr":    true,
	"isatty":    true,
	// The process's own ids — effective, real, and the supplementary
	// group set. Reading them reaches nothing:
	// the identity was chosen by whoever exec'd the program, and a
	// dependency that learns it gains no authority it did not have —
	// the same argument that leaves `isatty` ungated. Note this is
	// where the two capability systems part company: `internal/
	// platforms` DOES gate these, because there the question is
	// whether the target can answer at all (WASI cannot), not
	// whether a dependency should be allowed to ask.
	// The limits the kernel enforces on this process (`rlimit_nofile`)
	// join them by the same argument: the ceiling was chosen by whoever
	// exec'd the program, and a dependency that learns how many
	// descriptors it may open reaches nothing it could not already
	// reach. `internal/platforms` gates that one too, because there the
	// question is whether the target has resource limits at all.
	"geteuid":       true,
	"getegid":       true,
	"getuid":        true,
	"getgid":        true,
	"getgroups":     true,
	"rlimit_nofile": true,
	"target_os":     true,
	"target_arch":   true,
	"args":          true,
	"exit":          true,
	// A signal disposition reconfigures how THIS process reacts to
	// something delivered to it. It reaches nothing outside the
	// process and confers no authority a dependency could escalate
	// through — the same argument that leaves `exit` here, which is
	// also process-wide and also irreversible from a caller's view.
	// `internal/platforms` DOES gate these, because there the
	// question is whether the target has signals at all.
	"signal_ignore":               true,
	"signal_default":              true,
	"strbuf_reset":                true,
	"strbuf_append":               true,
	"strbuf_take":                 true,
	"f32_bits":                    true,
	"f32_from_bits":               true,
	"f64_bits":                    true,
	"f64_from_bits":               true,
	"poll":                        true,
	"wasm_block":                  true,
	"wasm_poll":                   true,
	"wasm_pollable_drop":          true,
	"map_new":                     true,
	"cell_new":                    true,
	"string_from_bytes_unchecked": true,
	"slice_unchecked":             true,
	"int_to_string":               true,
}
