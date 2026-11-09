package platforms

import (
	"fmt"
	"sort"

	"github.com/jakechampion/lang/internal/syntax/ast"
)

var gatedBuiltins = map[string]string{
	"subprocess": "subprocess",

	"proc_fork":           "proc",
	"proc_waitpid":        "proc",
	"proc_waitpid_nohang": "proc",
	"proc_waitpid_status": "proc",
	"__worker_region":     "proc",
	"__worker_exit":       "proc",
	"__worker_reap":       "proc",
	"__worker_unmap":      "proc",
	"proc_exec":           "proc",
	"proc_exec_as":        "proc",
	"pipe":                "proc",
	"process_alive":       "proc",
	"signal_send":         "proc",
	"set_process_group":   "proc",

	"rlimit_nofile":      "rlimit",
	"disable_core_dumps": "rlimit",

	"priority":     "sched",
	"set_priority": "sched",

	"statfs": "fsinfo",
	"mounts": "fsinfo",

	"__heap_mark":       "arena",
	"__heap_release_to": "arena",
	"__heap_adopt":      "arena",

	"stdin": "stdin",

	"print":   "log",
	"eprint":  "log",
	"stdout":  "stdout",
	"stderr":  "stdout",
	"write":   "stdout",
	"putchar": "stdout",

	"now_unix_ms":      "now",
	"now_ns":           "now",
	"monotonic_ns":     "now",
	"clock_resolution": "now",
	"sleep_ms":         "now",
	"sleep_ns":         "now",
	"clock_set":        "now",
	"poll":             "now",

	"timer_fd": "pollfd",

	"wasm_timer_pollable": "pollable",
	"wasm_pollable_drop":  "pollable",
	"wasm_block":          "pollable",
	"wasm_poll":           "pollable",

	"env":        "env",
	"environ":    "env",
	"args":       "args",
	"set_args":   "args",
	"config_get": "config",

	"random_bytes": "random",
	"random_i32":   "random",

	"tcp_listen":            "tcp",
	"tcp_accept":            "tcp",
	"tcp_accept_with":       "tcp",
	"tcp_local_port":        "tcp",
	"tcp_send_bytes":        "tcp",
	"tcp_sendfile":          "tcp",
	"tcp_send_buf":          "tcp",
	"tcp_close":             "tcp",
	"__tcp_close_unchecked": "tcp",
	"__fd_close":            "tcp",
	"__socket_from_fd":      "tcp",
	"tcp_pollable":          "tcp",
	"udp_bind":              "tcp",
	"udp_bind_with":         "tcp",
	"udp_connect":           "tcp",
	"udp_sendto":            "tcp",
	"udp_sendto_bytes":      "tcp",
	"udp_sendmmsg":          "mmsg",
	"udp_recvmmsg":          "mmsg",
	"udp_recvfrom":          "tcp",
	"tcp_listen_with":       "tcp",
	"tcp_socket_ctl":        "tcp",
	"tcp_addr_into":         "tcp",
	"tcp_connect_with":      "tcp",

	"unix_listen":  "unix",
	"unix_connect": "unix",

	"reactor_new":   "reactor",
	"reactor_ctl":   "reactor",
	"reactor_wait":  "reactor",
	"tcp_recv_into": "tcp",

	"ring_new":     "uring",
	"ring_ctl":     "uring",
	"ring_wait":    "uring",
	"ring_recv":    "uring",
	"ring_send_zc": "uring",
	"tcp_ktls":     "ktls",

	"read_file":        "fs",
	"read_file_bytes":  "fs",
	"write_file":       "fs",
	"write_file_bytes": "fs",
	"open_reader":      "fs",
	"open_writer":      "fs",
	"open_appender":    "fs",
	"open_exclusive":   "fs",
	"open_reader_with": "fs",
	"open_writer_with": "fs",
	"stat":             "fs",
	"lstat":            "fs",
	"read_dir":         "fs",
	"read_dir_bytes":   "fs",
	"read_dir_all":     "fs",
	"read_dir_ino":     "fs",
	"read_dir_ino_all": "fs",
	"remove_file":      "fs",
	"remove_dir_all":   "fs",
	"create_dir_all":   "fs",
	"create_dir":       "fs",
	"remove_dir":       "fs",
	"create_link":      "fs",
	"clone_file":       "fs",
	"create_symlink":   "fs",
	"read_link":        "fs",
	"read_link_bytes":  "fs",
	"rename":           "fs",
	"set_file_times":   "fs",
	"temp_dir":         "fs",
	"truncate":         "fs",

	"mknod": "fsnode",

	"sync":             "fssync",
	"rename_noreplace": "fsrename",
	"rename_exchange":  "fsrename",
	"open_dir":         "fsdir",
	"getxattr":         "xattr",
	"getxattr_bytes":   "xattr",
	"lgetxattr":        "xattr",
	"lgetxattr_bytes":  "xattr",
	"setxattr":         "xattr",
	"setxattr_bytes":   "xattr",
	"lsetxattr":        "xattr",
	"lsetxattr_bytes":  "xattr",
	"chown_at":         "fsowner",

	"write_file_exec": "fsmode",
	"access":          "fsmode",
	"umask":           "fsmode",
	"chmod":           "fsmode",
	"chmod_at":        "fsmode",

	"geteuid":   "userid",
	"getegid":   "userid",
	"getuid":    "userid",
	"getgid":    "userid",
	"getgroups": "userid",
	"setuid":    "userid",
	"setgid":    "userid",
	"setgroups": "userid",

	"signal_ignore":             "signal",
	"signal_default":            "signal",
	"signal_catch":              "signal",
	"signal_taken":              "signal",
	"signal_catch_interrupting": "signal",
	"signal_raise":              "signal",
	"signal_mask":               "signal",
	"signal_disposition":        "signal",

	"window_size":     "tty",
	"set_window_size": "tty",
	"termios_get":     "tty",
	"termios_set":     "tty",
	"set_extproc":     "tty",

	"hostname": "host",

	"uname_field": "sysinfo",
	"cpu_count":   "sysinfo",
	"prctl_query": "sysinfo",
	"sysctl":      "sysctl",

	"getcwd": "cwd",
	"chdir":  "cwd",
	"chroot": "cwd",

	"__c_call0": "cabi", "__c_call0_f32": "cabi", "__c_call0_f64": "cabi",
	"__c_call1": "cabi", "__c_call1_f32": "cabi", "__c_call1_f64": "cabi",
	"__c_call2": "cabi", "__c_call2_f32": "cabi", "__c_call2_f64": "cabi",
	"__c_call3": "cabi", "__c_call3_f32": "cabi", "__c_call3_f64": "cabi",
	"__c_call4": "cabi", "__c_call4_f32": "cabi", "__c_call4_f64": "cabi",
	"__c_call5": "cabi", "__c_call5_f32": "cabi", "__c_call5_f64": "cabi",
	"__c_call6": "cabi", "__c_call6_f32": "cabi", "__c_call6_f64": "cabi",
	"__c_callv":  "cabi",
	"__syscall3": "syscall", "__syscall4": "syscall", "__syscall5": "syscall", "__syscall6": "syscall",
	"__raw_alloc": "rawmem", "__raw_string": "rawmem", "__raw_arr_box": "rawmem",
	"__raw_scratch": "rawmem", "__raw_environ": "rawmem", "__raw_splice_pipe": "rawmem",
	"__raw_cover": "rawmem", "__raw_task_state": "rawmem", "__raw_vdso": "rawmem",
	"__raw_vg_request": "rawmem",
	"__wfi":            "privileged", "__hvc": "privileged", "__smc": "privileged",
	"__boot_info": "privileged", "__sysreg_read": "privileged", "__sysreg_write": "privileged",
	"__cache_clean": "privileged", "__cache_flush": "privileged", "__cache_sync_code": "privileged", "__tlb_flush": "privileged", "__tlb_flush_all": "privileged",
	"__breakpoint": "privileged",
	"__irq_mask":   "privileged", "__irq_unmask": "privileged", "__irq_wait": "privileged", "__irq_pending": "privileged", "__user_run": "privileged",
	"__port_in8": "privileged", "__port_in16": "privileged", "__port_in32": "privileged",
	"__port_out8": "privileged", "__port_out16": "privileged", "__port_out32": "privileged",
	"__mem_load8": "privileged", "__mem_load16": "privileged", "__mem_load32": "privileged", "__mem_load64": "privileged",
	"__mem_store8": "privileged", "__mem_store16": "privileged", "__mem_store32": "privileged", "__mem_store64": "privileged",
	"__mmio_load8": "mmio", "__mmio_load16": "mmio", "__mmio_load32": "mmio", "__mmio_load64": "mmio",
	"__mmio_store8": "mmio", "__mmio_store16": "mmio", "__mmio_store32": "mmio", "__mmio_store64": "mmio",
}

var coreBuiltins = map[string]bool{
	"__outer_mul_f64":     true,
	"__inner_mul_add_f64": true,
	"exit":                true,
	"isatty":              true,
	"target_os":           true,
	"target_arch":         true,
	"__account_entry":     true,
	"__ct_secret":         true,
	"__ct_public":         true,
	"__bytes_owned":       true,
	"__bytes_range":       true,
	"__bytes_into":        true,
	"__aes_expand_key":    true,
	"__aes_ctr32":         true,
	"__ghash":             true,
	"__aes_gcm_seal_into": true,
	"__aes_gcm_open_into": true,
	"__sha256_hw":         true,
	"__sha256_hw_blocks":  true,
	"__sha1_hw":           true,
	"__sha1_hw_blocks":    true,

	"map_new":                     true,
	"cell_new":                    true,
	"__send_copy":                 true,
	"__send_adopt":                true,
	"__send_root":                 true,
	"__send_release":              true,
	"string_from_bytes_unchecked": true,
	"slice_unchecked":             true,

	"buf_new":                 true,
	"buf_push":                true,
	"buf_push_range":          true,
	"buf_push_mapped":         true,
	"buf_push_bytes_mapped":   true,
	"buf_push_bytes_filtered": true,
	"buf_push_bytes_expanded": true,
	"buf_push_filtered":       true,
	"buf_push_expanded":       true,
	"buf_push_byte":           true,
	"buf_push_u64":            true,
	"buf_push_decimal":        true,
	"buf_push_base64":         true,
	"buf_push_base32":         true,
	"buf_decode_base32":       true,
	"buf_len":                 true,
	"buf_take":                true,
	"buf_take_bytes":          true,
	"buf_push_bytes_range":    true,
	"buf_free":                true,
	"buf_clear":               true,

	"f32_bits":      true,
	"f32_from_bits": true,
	"f64_bits":      true,
	"f64_from_bits": true,

	"string_from_bytes_range_unchecked": true,
	"share_unchecked":                   true,
}

func CoreBuiltin(name string) bool { return coreBuiltins[name] }

func GatedBuiltin(name string) (string, bool) {
	c, ok := gatedBuiltins[name]
	return c, ok
}

func TargetsProviding(capability string) []string {
	var out []string
	for name := range table {
		if HasCapability(name, capability) {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

type Violation struct {
	Builtin    string
	Capability string
	Target     string
	Pos        ast.Position
	FuncName   string
	FuncModule string
}

func (v Violation) Message(entryModule string) string {
	where := ""
	if v.FuncModule != "" && v.FuncModule != entryModule {
		where = fmt.Sprintf(" (reached via %q from module %q)", v.FuncName, v.FuncModule)
	}
	msg := fmt.Sprintf("target %q does not provide `%s`, required by `%s`%s; targets providing it: %v",
		v.Target, v.Capability, v.Builtin, where, TargetsProviding(v.Capability))
	if _, ok := providerSignatures[v.Capability]; ok {
		msg += fmt.Sprintf("; or import a module with a `@provide(\"%s\")` function", v.Capability)
	}
	if alt, ok := equivalents[v.Builtin]; ok && HasCapability(v.Target, gatedBuiltins[alt]) {
		msg += fmt.Sprintf("; use `%s`, which this target provides", alt)
	}
	return msg
}

var equivalents = map[string]string{"timer_fd": "wasm_timer_pollable", "wasm_timer_pollable": "timer_fd"}

// providerSignatures holds each capability that a module can implement with
// @provide, and the signature of its provider. A `log` provider is a byte sink
// that does not allocate. An `exit` provider ends the run of a target whose
// entry is not a process.
var providerSignatures = map[string]string{"log": "(data: usize, len: i32): void", "exit": "(code: i32): void"}

// providerParams reports whether params are the parameters of a provider of
// capability.
func providerParams(capability string, params []ast.Param) bool {
	if capability == "exit" {
		return len(params) == 1 && params[0].Type.String() == "i32"
	}
	return len(params) == 2 && params[0].Type.String() == "usize" && params[1].Type.String() == "i32"
}

// ProviderUsed reports whether the program's provider of capability is the
// implementation on target. The target's own implementation wins: a `log`
// provider serves a target that does not grant `log`, and an `exit` provider
// serves a target whose entry is not a process. compiler/platforms.fern's
// provider_used holds the same rule.
func ProviderUsed(target, capability string) bool {
	if capability == "exit" {
		return ForTarget(target).Entry.OrDefault() != EntryProcess
	}
	return !HasCapability(target, capability)
}

// Provided reports the capabilities that the program's providers add to the
// target's set.
func Provided(prog *ast.Program) map[string]bool {
	out := map[string]bool{}
	for _, fn := range prog.Funcs {
		if _, ok := providerSignatures[fn.Provides]; ok {
			out[fn.Provides] = true
		}
	}
	return out
}

// ProviderEntries names each provider of a capability that the target does not
// grant. The runtime calls it, so it is a root of the tree-shake.
func ProviderEntries(prog *ast.Program, target string) []string {
	var out []string
	for _, fn := range prog.Funcs {
		if fn.Provides != "" && ProviderUsed(target, fn.Provides) {
			out = append(out, fn.Name)
		}
	}
	return out
}

// ProviderProblem is an E101: a capability that takes no provider, a provider
// with the wrong signature, or two providers of one capability.
type ProviderProblem struct {
	Pos ast.Position
	Msg string
}

func ProviderProblems(prog *ast.Program) []ProviderProblem {
	var out []ProviderProblem
	first := map[string]string{}
	for _, fn := range prog.Funcs {
		if fn.Provides == "" {
			continue
		}
		msg := providerProblem(fn)
		if prev, dup := first[fn.Provides]; msg == "" && dup {
			msg = fmt.Sprintf("capability `%s` has two providers, %q and %q", fn.Provides, prev, fn.Name)
		}
		if msg != "" {
			out = append(out, ProviderProblem{Pos: fn.P, Msg: msg})
			continue
		}
		first[fn.Provides] = fn.Name
	}
	return out
}

func providerProblem(fn *ast.FuncDecl) string {
	sig, ok := providerSignatures[fn.Provides]
	if !ok {
		return fmt.Sprintf("`@provide(%q)`: no capability `%s` takes a provider; a provider can implement `log` or `exit`", fn.Provides, fn.Provides)
	}
	ret := "void"
	if fn.ReturnType != nil {
		ret = fn.ReturnType.String()
	}
	if fn.Receiver == nil && len(fn.TypeParams) == 0 && ret == "void" && providerParams(fn.Provides, fn.Params) {
		return ""
	}
	return fmt.Sprintf("the `%s` provider %q must be a function with the signature %s", fn.Provides, fn.Name, sig)
}

func Enforce(prog *ast.Program, target string) []Violation {
	d := ForTarget(target)
	if d == nil {
		return nil
	}
	granted := Provided(prog)
	for _, c := range d.Capabilities {
		granted[c] = true
	}
	var out []Violation
	scanGatedCalls(prog, func(fn *ast.FuncDecl, call *ast.Call, builtin, capName string) {
		if granted[capName] {
			return
		}
		out = append(out, Violation{
			Builtin:    builtin,
			Capability: capName,
			Target:     target,
			Pos:        call.P,
			FuncName:   fn.Name,
			FuncModule: fn.BodyModule(),
		})
	})
	return out
}

func scanGatedCalls(prog *ast.Program, visit func(fn *ast.FuncDecl, call *ast.Call, builtin, capability string)) {
	declared := map[string]bool{}
	for _, fn := range prog.Funcs {
		if fn.Receiver == nil && fn.MethodRecv == "" && fn.ImplTrait == "" {
			declared[fn.Name] = true
		}
	}
	for _, fn := range prog.Funcs {
		if fn.Body == nil {
			continue
		}
		seen := map[string]bool{}
		ast.Walk(fn.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.Call)
			if !ok {
				return true
			}
			id, ok := call.Callee.(*ast.Ident)
			if !ok || declared[id.Name] {
				return true
			}
			capName, gated := gatedBuiltins[id.Name]
			if !gated || seen[id.Name] {
				return true
			}
			seen[id.Name] = true
			visit(fn, call, id.Name, capName)
			return true
		})
	}
}

func Reach(prog *ast.Program) map[string]string {
	out := map[string]string{}
	scanGatedCalls(prog, func(_ *ast.FuncDecl, _ *ast.Call, builtin, capName string) {
		if prev, ok := out[capName]; !ok || builtin < prev {
			out[capName] = builtin
		}
	})
	return out
}

func GatedBuiltins() map[string]string {
	out := make(map[string]string, len(gatedBuiltins))
	for k, v := range gatedBuiltins {
		out[k] = v
	}
	return out
}
