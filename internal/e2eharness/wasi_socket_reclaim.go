package e2eharness

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

// WasiSocketStorageProbe repeats a complete operation, preserving its result
// while checking that the guest heap stops growing after the first iteration.
// The host fault fixture runs the same loop at every failure stage.
func WasiSocketStorageProbe(expr string, closeSocket bool) string {
	close := ""
	if closeSocket {
		close = "if (h >= 0) { result = tcp_close(h); }"
	}
	return `function main(): i32 {
    var i: i32 = 0;
    var stable: i64 = 0;
    var result: i32 = 0;
    while (i < 32) {
        var allocations: i64 = __heap_alloc_count();
        var h: i32 = ` + expr + `;
        result = h;
        ` + close + `
        var used: i64 = __heap_bump_bytes();
        if (i == 0) { stable = used; }
        if (used != stable) { return -1000; }
        if (__heap_alloc_count() - allocations != 1) { return -1001; }
        i = i + 1;
    }
    return result;
}`
}

// WasiUDPStorageProbe covers both inline-string spills and borrowed string
// buffers. Normalize successful byte counts for the host ownership oracle.
func WasiUDPStorageProbe(host, data string) string {
	return fmt.Sprintf(`function main(): i32 {
    var host: string = %q;
    var data: string = %q;
    var i: i32 = 0;
    var stable: i64 = 0;
    var result: i32 = 0;
    while (i < 32) {
        result = udp_send(host, 1, data);
        if (result >= 0) {
            if (result != data.len()) { return -1002; }
            result = 1;
        }
        var used: i64 = __heap_bump_bytes();
        if (i == 0) { stable = used; }
        if (used != stable) { return -1000; }
        if (host != %q || data != %q) { return -1003; }
        i = i + 1;
    }
    return result;
}`, host, data, host, data)
}

// CheckWasiSocketReclaim replaces only host imports in a compiled core module.
// The production socket bodies and allocator still execute. Host resources are
// tracked independently; dropping an unowned handle or a parent before its
// children traps. Each setup phase can report unknown (error-code zero).
func CheckWasiSocketReclaim(t *testing.T, modulePath, operation string) {
	t.Helper()
	invalidHost := operation == "udp-invalid"
	if invalidHost {
		operation = "udp"
	}
	closing := strings.HasSuffix(operation, "-close")
	operation = strings.TrimSuffix(operation, "-close")
	for _, tool := range []string{"wasm-tools", "wasmtime"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skip(tool + " not on PATH")
		}
	}
	out, err := exec.Command("wasm-tools", "print", modulePath).CombinedOutput()
	if err != nil {
		t.Fatalf("print core module: %v\n%s", err, out)
	}
	wat := string(out)
	export := regexp.MustCompile(`\(export "main" \(func ([^\s)]+)\)\)`).FindStringSubmatch(wat)
	if len(export) != 2 {
		// The self-host command framing exports _start, with a private $main.
		export = regexp.MustCompile(`\(func (\$main)\s`).FindStringSubmatch(wat)
	}
	if len(export) != 2 {
		t.Fatal("core module has no main export")
	}
	// wasm-tools encloses numeric index comments in parentheses.
	imports := regexp.MustCompile(`\(import "([^"]+)" "([^"]+)" \(func ((?:\$[^\s()]+\s+)?\(;[0-9]+;\)) \(type ([0-9]+)\)\)\)`)
	seen := 0
	wat = imports.ReplaceAllStringFunc(wat, func(decl string) string {
		m := imports.FindStringSubmatch(decl)
		seen++
		body := socketHostBody(operation, m[1], m[2])
		return "(func " + m[3] + " (type " + m[4] + ") " + body + ")"
	})
	if seen == 0 || strings.Contains(wat, "(import ") {
		t.Fatal("unhandled core import syntax")
	}
	steps := map[string]int{"listen": 5, "connect": 3, "accept": 1, "port": 1, "udp": 6}[operation]
	if steps == 0 {
		t.Fatal("unknown socket operation")
	}
	// Successful TCP setup transfers ownership to the returned connection.
	// UDP sends are one-shot and release everything before returning.
	socket, streams := 1, 0
	successFailure := "(i32.le_s (local.get $result) (i32.const 0))"
	if operation == "connect" || operation == "accept" {
		streams = 1
	}
	if operation == "udp" {
		socket = 0
		successFailure = "(i32.ne (local.get $result) (i32.const 1))"
	}
	if operation == "port" {
		socket = 0
		successFailure = "(i32.ne (local.get $result) (select (i32.const 0) (i32.const 65535) (i32.eqz (global.get $fh_handle))))"
	}
	if closing {
		socket, streams = 0, 0
		successFailure = "(i32.ne (local.get $result) (i32.const 0))"
	}
	if invalidHost {
		steps, socket, streams = 0, 0, 0
		successFailure = "(i32.ne (local.get $result) (i32.const -28))"
	}
	borrowedListener := ""
	if operation == "accept" || operation == "port" {
		// A borrowed listener record at address zero, with host handle 42.
		// The accepted socket gets a distinct handle (0 or 7) and owns its
		// streams. The listener remains host-owned and must never be dropped.
		borrowedListener = "(i32.store (i32.const 0) (i32.const 42))"
	}
	probe := fmt.Sprintf(`
  (global $fh_fail (mut i32) (i32.const 0))
  (global $fh_handle (mut i32) (i32.const 0))
  (global $fh_socket (mut i32) (i32.const 0))
  (global $fh_in (mut i32) (i32.const 0))
  (global $fh_out (mut i32) (i32.const 0))
  (global $fh_poll (mut i32) (i32.const 0))
  (func (export "fault_probe") (param $step i32) (param $handle i32) (result i32)
    (local $result i32)
    (global.set $fh_fail (local.get $step))
    (global.set $fh_handle (local.get $handle))
    %s
    (local.set $result (call %s))
    (if (local.get $step) (then
      (if (i32.ne (local.get $result) (i32.const -29)) (then (return (i32.const 1)))))
    (else
      (if %s (then (return (i32.const 2))))))
    (if (i32.ne (global.get $fh_socket) (select (i32.const 0) (i32.const %d) (local.get $step))) (then (return (i32.const 3))))
    (if (i32.ne (global.get $fh_in) (select (i32.const 0) (i32.const %d) (local.get $step))) (then (return (i32.const 4))))
    (if (i32.ne (global.get $fh_out) (select (i32.const 0) (i32.const %d) (local.get $step))) (then (return (i32.const 5))))
    (if (global.get $fh_poll) (then (return (i32.const 6))))
    (i32.const 0))
`, borrowedListener, export[1], successFailure, socket, streams, streams)
	end := strings.LastIndex(wat, ")")
	path := filepath.Join(t.TempDir(), "socket-faults.wat")
	if err := os.WriteFile(path, []byte(wat[:end]+probe+")"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, handle := range []int{0, 7} {
		for step := 0; step <= steps; step++ {
			t.Run(fmt.Sprintf("handle%d/step%d", handle, step), func(t *testing.T) {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				cmd := exec.CommandContext(ctx, "wasmtime", "run", "--invoke", "fault_probe", path, strconv.Itoa(step), strconv.Itoa(handle))
				got, err := cmd.Output()
				if err != nil {
					if ee, ok := err.(*exec.ExitError); ok {
						t.Fatalf("fault probe: %v\n%s", err, ee.Stderr)
					}
					t.Fatal(err)
				}
				if strings.TrimSpace(string(got)) != "0" {
					t.Errorf("fault probe returned %q (1=result, 2=success, 3=socket, 4=input, 5=output, 6=pollable)", got)
				}
			})
		}
	}
}

func socketHostBody(operation, moduleName, name string) string {
	if strings.HasPrefix(moduleName, "wasi:sockets/instance-network@") {
		return "(i32.const 0)"
	}
	if strings.Contains(name, "[resource-drop]") {
		kind := ""
		switch {
		case strings.HasSuffix(name, "tcp-socket"), strings.HasSuffix(name, "udp-socket"):
			kind = "socket"
		case strings.HasSuffix(name, "input-stream"), strings.HasSuffix(name, "incoming-datagram-stream"):
			kind = "in"
		case strings.HasSuffix(name, "output-stream"), strings.HasSuffix(name, "outgoing-datagram-stream"):
			kind = "out"
		case strings.HasSuffix(name, "pollable"):
			kind = "poll"
		}
		if kind == "" {
			return "unreachable"
		}
		body := fmt.Sprintf("(if (i32.or (i32.ne (local.get 0) (global.get $fh_handle)) (i32.eqz (global.get $fh_%s))) (then unreachable)) (global.set $fh_%s (i32.const 0))", kind, kind)
		if kind == "socket" {
			body += " (if (i32.or (global.get $fh_in) (i32.or (global.get $fh_out) (global.get $fh_poll))) (then unreachable))"
		}
		return body
	}
	if strings.HasSuffix(name, ".subscribe") {
		return "(global.set $fh_poll (i32.const 1)) (global.get $fh_handle)"
	}
	if strings.HasSuffix(name, "pollable.block") {
		return "(if (i32.eqz (global.get $fh_poll)) (then unreachable))"
	}
	phase, ret, own := 0, 1, ""
	switch {
	case strings.HasPrefix(name, "create-"):
		phase, own = 1, "(global.set $fh_socket (i32.const 1))"
	case strings.HasSuffix(name, ".start-bind"), strings.HasSuffix(name, ".start-connect"):
		phase, ret = 2, 14
	case strings.HasSuffix(name, ".finish-bind"):
		phase = 3
	case strings.HasSuffix(name, ".finish-connect"):
		phase, own = 3, "streams"
	case strings.HasSuffix(name, ".accept"):
		phase, own = 1, "accepted"
	case strings.HasSuffix(name, ".local-address"):
		phase = 1
	case strings.HasSuffix(name, ".start-listen"):
		phase = 4
	case strings.HasSuffix(name, ".finish-listen"):
		phase = 5
	case strings.HasSuffix(name, ".stream"):
		phase, ret, own = 4, 14, "streams"
	case strings.HasSuffix(name, ".check-send"):
		phase = 5
	case strings.HasSuffix(name, ".send"):
		phase, ret = 6, 3
	}
	if phase == 0 {
		return "unreachable"
	}
	if own == "streams" {
		own = "(global.set $fh_in (i32.const 1)) (global.set $fh_out (i32.const 1))"
	}
	if own == "accepted" {
		own = "(global.set $fh_socket (i32.const 1)) (global.set $fh_in (i32.const 1)) (global.set $fh_out (i32.const 1))"
	}
	payload := fmt.Sprintf("(i32.store offset=4 (local.get %d) (global.get $fh_handle)) (i32.store offset=8 (local.get %d) (global.get $fh_handle))", ret, ret)
	if operation == "port" {
		// IPv4 port zero and IPv6 port 65535 exercise both canonical shapes.
		payload = "(if (i32.ne (local.get 0) (i32.const 42)) (then unreachable)) " +
			"(i32.store offset=4 (local.get 1) (i32.ne (global.get $fh_handle) (i32.const 0))) " +
			"(i32.store16 offset=8 (local.get 1) (select (i32.const 0) (i32.const 65535) (i32.eqz (global.get $fh_handle))))"
	}
	if operation == "accept" {
		payload += fmt.Sprintf(" (i32.store offset=12 (local.get %d) (global.get $fh_handle))", ret)
	}
	if operation == "udp" && phase >= 5 {
		payload = fmt.Sprintf("(i64.store offset=8 (local.get %d) (i64.const 1))", ret)
	}
	return fmt.Sprintf("(if (i32.eq (global.get $fh_fail) (i32.const %d)) (then (i32.store (local.get %d) (i32.const 1)) (i32.store offset=4 (local.get %d) (i32.const 0))) (else (i32.store (local.get %d) (i32.const 0)) %s %s))", phase, ret, ret, ret, payload, own)
}
