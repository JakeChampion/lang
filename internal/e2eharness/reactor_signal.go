package e2eharness

// ReactorSignalProbe exercises the signal pollable of the Driver's reactor
// (#9853): SIGUSR1 watched, delivered by a child shell to this process and
// reported as the pair (-signal, 1), consumed, delivered and reported
// again, then unwatched and watched once more. `mode` is how the child is
// run: "interp" through subprocess, which only the interpreter provides,
// "native" through proc_fork and proc_exec, and "wasm" checks the -ENOTSUP
// a target without signals answers. Exit 42 and "ok" on stdout iff every
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
	if mode == "wasm" {
		body = `    if (drv.watch_signal(10) != 0 - 58) { return fail(1); }
`
	}
	return `import "core/int";
import "std/async";

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
