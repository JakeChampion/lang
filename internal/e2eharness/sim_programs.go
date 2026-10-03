package e2eharness

// SimNetProgram exercises the same SimNet contracts as the TAP
// suite without std/test (whose fs assertion helpers keep every TAP
// file interp/self-host-gated — #5372). Exit 42 iff every check holds.
const SimNetProgram = `import "std/async";
import "std/time";
import "std/sim";

function main(): i32 {
    let d: sim.Sim = sim.new(1);
    let n: sim.Net = sim.net(d);
    n = n.serve(1, 80, "/k", "primary", time.duration_nanos(30000000 as i64));
    n = n.serve(2, 80, "/k", "cache", time.duration_nanos(10000000 as i64));
    n = n.serve(3, 80, "/k", "mirror", time.duration_nanos(20000000 as i64));
    let fs: async.Future[string][] = [
        n.fetch_future(1, 80, "/k"),
        n.fetch_future(2, 80, "/k"),
        n.fetch_future(3, 80, "/k"),
        n.fetch_future(9, 80, "/k")
    ];
    let got: string[] = async.gather_on(d, fs, "!");
    if (got[0] != "primary" || got[1] != "cache" || got[2] != "mirror") { return 1; }
    if (got[3] != "") { return 2; }
    if (d.now_ns() != 30000000) { return 3; }
    if (n.hits(2, 80, "/k") != 1) { return 4; }
    if (n.hits(9, 80, "/k") != 0) { return 5; }

    let rd: sim.Sim = sim.new(1);
    let rn: sim.Net = sim.net(rd);
    rn = rn.serve(1, 80, "/k", "slow", time.duration_nanos(40000000 as i64));
    rn = rn.serve(2, 80, "/k", "fast", time.duration_nanos(10000000 as i64));
    let rf: async.Future[string][] = [
        rn.fetch_future(1, 80, "/k"),
        rn.fetch_future(2, 80, "/k")
    ];
    let (w, v) = async.race_on(rd, rf, "!");
    if (w != 1 || v != "fast") { return 6; }
    if (rd.now_ns() != 10000000) { return 7; }

    let dd: sim.Sim = sim.new(7);
    let dn: sim.Net = sim.net(dd);
    dn = dn.serve(1, 80, "/k", "late", time.duration_nanos(40000000 as i64));
    dn = dn.serve(2, 80, "/k", "early", time.duration_nanos(10000000 as i64));
    let df: async.Future[string][] = [
        dn.fetch_future(1, 80, "/k"),
        dn.fetch_future(2, 80, "/k")
    ];
    let dl: Option[string][] = async.with_deadline_on(dd, time.duration_millis(25), df);
    match (dl[0]) { Some(x) => { return 8; }, None => { } }
    match (dl[1]) { Some(x) => { if (x != "early") { return 9; } }, None => { return 10; } }
    if (dd.now_ns() != 25000000) { return 11; }

    let cd: sim.Sim = sim.new(1);
    let cn: sim.Net = sim.net(cd);
    cn = cn.serve_chunked(1, 80, "/big", "abcdefghij", time.duration_nanos(5000000 as i64), time.duration_nanos(5000000 as i64), sim.chunks_of(10, 4));
    let cf: async.Future[string][] = [cn.fetch_future(1, 80, "/big")];
    let cb: string[] = async.gather_on(cd, cf, "!");
    if (cb[0] != "abcdefghij") { return 12; }
    if (cd.now_ns() != 15000000) { return 13; }
    return 42;
}
`

// SimFaultProgram exercises the same fault contracts as the TAP
// suite without std/test (whose fs assertion helpers keep every TAP
// file interp/self-host-gated — #5372). Exit 42 iff every check holds.
// The flaky(50) seed-1 pattern "SSSFFSSFSF" is the cross-backend
// determinism golden: pure integer arithmetic, so the identical
// program must produce the identical run on interp / native / wasm.
const SimFaultProgram = `import "std/async";
import "std/time";
import "std/sim";

function flaky_pattern(seed: i64): string {
    let d: sim.Sim = sim.new(seed);
    let n: sim.Net = sim.net(d);
    n = n.serve(1, 80, "/k", "body", time.duration_nanos(10000000 as i64));
    n = n.fault_flaky(1, 80, "/k", 50);
    let out: string = "";
    let i: i32 = 0;
    while (i < 10) {
        match (n.fetch_future(1, 80, "/k")) {
            Ready(v) => { out = out + "F"; },
            Pending(tok, c) => { out = out + "S"; },
        }
        i = i + 1;
    }
    return out;
}

function gather_shape_ok(seed: i64): boolean {
    let d: sim.Sim = sim.new(seed);
    let n: sim.Net = sim.net(d);
    n = n.serve(1, 80, "/k", "alpha", time.duration_nanos(10000000 as i64));
    n = n.serve(2, 80, "/k", "beta", time.duration_nanos(20000000 as i64));
    n = n.serve(3, 80, "/k", "gamma", time.duration_nanos(5000000 as i64));
    n = n.fault_flaky(2, 80, "/k", 50);
    n = n.fault_stall(3, 80, "/k");
    let fs: async.Future[string][] = [
        n.fetch_future(1, 80, "/k"),
        n.fetch_future(2, 80, "/k"),
        n.fetch_future(3, 80, "/k")
    ];
    let got: string[] = async.gather_on(d, fs, "!");
    if (got.len() != 3) { return false; }
    if (got[0] != "alpha") { return false; }
    if (got[1] != "beta" && got[1] != "") { return false; }
    return got[2] == "!";
}

function flaky_first_call_ok(seed: i64): boolean {
    let d: sim.Sim = sim.new(seed);
    let n: sim.Net = sim.net(d);
    n = n.serve(1, 80, "/k", "body", time.duration_nanos(10000000 as i64));
    n = n.fault_flaky(1, 80, "/k", 50);
    let ok: boolean = false;
    match (n.fetch_future(1, 80, "/k")) {
        Ready(v) => { },
        Pending(tok, c) => { ok = true; },
    }
    return ok;
}

function main(): i32 {
    let fd: sim.Sim = sim.new(1);
    let fn2: sim.Net = sim.net(fd);
    fn2 = fn2.serve(1, 80, "/k", "body", time.duration_nanos(10000000 as i64));
    fn2 = fn2.fault_fail(1, 80, "/k");
    match (fn2.fetch_future(1, 80, "/k")) {
        Ready(v) => { if (v != "") { return 1; } },
        Pending(t, c) => { return 2; },
    }
    if (fd.now_ns() != 0) { return 3; }
    if (fn2.hits(1, 80, "/k") != 1) { return 4; }

    let d: sim.Sim = sim.new(7);
    let n: sim.Net = sim.net(d);
    n = n.serve(1, 80, "/k", "healthy", time.duration_nanos(10000000 as i64));
    n = n.serve(2, 80, "/k", "silent", time.duration_nanos(5000000 as i64));
    n = n.fault_stall(2, 80, "/k");
    let fs: async.Future[string][] = [
        n.fetch_future(1, 80, "/k"),
        n.fetch_future(2, 80, "/k")
    ];
    let got: Option[string][] = async.with_deadline_on(d, time.duration_millis(25), fs);
    match (got[0]) { Some(v) => { if (v != "healthy") { return 5; } }, None => { return 6; } }
    match (got[1]) { Some(v) => { return 7; }, None => { } }
    if (d.now_ns() != 25000000) { return 8; }

    let gd: sim.Sim = sim.new(1);
    let gn: sim.Net = sim.net(gd);
    gn = gn.serve(1, 80, "/k", "ok", time.duration_nanos(10000000 as i64));
    gn = gn.serve(2, 80, "/k", "gone", time.duration_nanos(5000000 as i64));
    gn = gn.fault_stall(2, 80, "/k");
    let gfs: async.Future[string][] = [
        gn.fetch_future(1, 80, "/k"),
        gn.fetch_future(2, 80, "/k")
    ];
    let g: string[] = async.gather_on(gd, gfs, "!");
    if (g[0] != "ok" || g[1] != "!") { return 9; }

    let pd: sim.Sim = sim.new(1);
    let pn: sim.Net = sim.net(pd);
    pn = pn.serve_chunked(1, 80, "/big", "abcdefghij", time.duration_nanos(5000000 as i64), time.duration_nanos(5000000 as i64), sim.chunks_of(10, 4));
    pn = pn.fault_partial(1, 80, "/big", 2);
    let f: async.Future[string] = pn.fetch_future(1, 80, "/big");
    let toks: i32[] = [];
    let silent: boolean = false;
    let guard: i32 = 0;
    while (guard < 10 && !silent) {
        match (f) {
            Ready(v) => { return 10; },
            Pending(tok, resume) => {
                if (tok < 0) {
                    silent = true;
                } else {
                    toks = toks.append(tok);
                    f = resume(tok);
                }
            },
        }
        guard = guard + 1;
    }
    if (!silent) { return 11; }
    if (toks.len() != 2) { return 12; }
    if (toks[0] != 5 || toks[1] != 10) { return 13; }

    // Golden sequences, re-recorded when sim's generator moved from
    // Park-Miller + biased modulo to std/rand's PCG32 + Lemire rejection
    // (#6193). Equal seeds still replay bit-identically -- that contract is
    // unchanged and is what checks 15/16 guard -- but WHICH sequence a given
    // seed produces is different, by design. Re-record, don't relax: a golden
    // string is how a silent change to the draw order gets caught.
    if (flaky_pattern(1) != "FFFFFSFFFS") { return 14; }
    if (flaky_pattern(9) != flaky_pattern(9)) { return 15; }
    if (flaky_pattern(1) == flaky_pattern(2)) { return 16; }

    if (sim.sweep_seeds(20, gather_shape_ok) != 0) { return 17; }
    // Also re-recorded: seed 1 now takes the Ready branch on its first draw.
    if (sim.sweep_seeds(20, flaky_first_call_ok) != 1) { return 18; }
    return 42;
}
`
