package e2eharness

// ReactorSignalProbe exercises the signal pollable of the Driver's reactor
// (#9853): SIGUSR1 watched, delivered by a child shell to this process and
// reported as the pair (-signal, 1), consumed, delivered and reported
// again, then unwatched and watched once more. `mode` is how the child is
// run: "interp" through subprocess, which only the interpreter provides,
// "native" through proc_fork and proc_exec, and "wasm" checks the -ENOTSUP
// a target without signals answers. Natively the parent's exit is reported
// too: a grandchild watches its parent, which exits a second after forking
// it, and connects back to the probe's listener once its wait reports the
// exit as (-15, 1); the interpreter, which cannot fork, checks the watch
// is taken. Exit 42 and "ok" on stdout iff every
// check holds, else the number of the first failing check.
func ReactorSignalProbe(mode string) string {
	deliver := `function deliver(): i32 {
    var p = subprocess("/bin/sh", ["-c", "kill -USR1 $PPID"], "");
    return p.exit_code;
}
`
	if mode == "native" {
		deliver = `function deliver(): i32 {
    var child: i32 = proc_fork();
    if (child == 0) {
        proc_exec("/bin/sh", ["-c", "kill -USR1 $PPID"]);
        exit(1);
    }
    if (child < 0) { return child; }
    return proc_waitpid(child);
}

function watcher(port: i32): i32 {
    var d: async.RealDriver = async.real_driver();
    if (d.watch_parent() != 0) { return 1; }
    var got: i32[] = d.wait(2, 10000);
    if (got.len() < 2 || got[0] != 0 - 15 || got[1] != 1) { return 2; }
    match (net.connect(net.socket_addr(net.ipv4_loopback(), port))) { Ok(fd) => { tcp_close(fd); return 0; }, Err(e) => { return 3; } }
    return 3;
}

function parent_exit(drv: async.RealDriver): i32 {
    var ln: i32 = 0;
    match (net.listen_with(0, net.listen_options())) { Ok(fd) => { ln = fd; }, Err(e) => { return 1; } }
    var port: i32 = tcp_local_port(ln);
    var child: i32 = proc_fork();
    if (child == 0) {
        if (proc_fork() == 0) { exit(watcher(port)); }
        sleep_ms(1000 as i64);
        exit(0);
    }
    if (child < 0 || proc_waitpid(child) != 0) { return 2; }
    if (drv.watch(ln, 1) != 0) { return 3; }
    var got: i32[] = drv.wait(2, 15000);
    if (got.len() < 2 || got[0] != ln) { return 4; }
    match (net.accept(ln)) { Ok(fd) => { tcp_close(fd); }, Err(e) => { return 5; } }
    drv.unwatch(ln);
    tcp_close(ln);
    return 0;
}
`
	}
	if mode == "wasm" {
		deliver = ""
	}
	body := `    if (drv.watch_signal(sigusr1()) != 0) { return fail(2); }
    if (drv.wait(2, 20).len() != 0) { return fail(3); }
    if (deliver() != 0) { return fail(4); }
    var got: i32[] = drv.wait(2, 2000);
    if (got.len() < 2 || got[0] != 0 - sigusr1() || got[1] != 1) { return fail(5); }
    if (drv.wait(2, 20).len() != 0) { return fail(6); }
    if (deliver() != 0) { return fail(4); }
    got = drv.wait(2, 2000);
    if (got.len() < 2 || got[0] != 0 - sigusr1()) { return fail(7); }
    if (drv.unwatch_signal(sigusr1()) != 0) { return fail(8); }
    if (drv.watch_signal(sigusr1()) != 0) { return fail(9); }
    if (drv.unwatch_signal(sigusr1()) != 0) { return fail(10); }
`
	imports := `import "core/int";
import "std/async";
`
	switch mode {
	case "native":
		body += `    if (parent_exit(drv) != 0) { return fail(12); }
`
		imports += `import "std/net";
`
	case "interp":
		body += `    if (drv.watch_parent() != 0) { return fail(12); }
`
	case "wasm":
		body = `    if (drv.watch_signal(10) != 0 - 58) { return fail(1); }
    if (drv.watch_parent() != 0 - 58) { return fail(12); }
`
	}
	return imports + `
function fail(n: i32): i32 {
    print(int.int_to_string(n));
    return n;
}
` + deliver + `

function sigusr1(): i32 {
    if (target_os() == "darwin") { return 30; }
    return 10;
}

function main(): i32 {
    var drv: async.RealDriver = async.real_driver();
` + body + `    if (drv.close() != 0) { return fail(11); }
    print("ok");
    return 42;
}
`
}
