package e2eharness

// SimTasksProgram is the sim's task gate (docs/NET-P3-SUSPENSION-PLAN.md
// §3.8): three handlers on a SimPlatform fetch scripted upstreams through
// plat.http, run together by sim.run_tasks. Each connect takes 5 ms and the
// answers come 30, 10 and 50 ms after the request, so under the self-host
// compiler every plat.http parks and the handlers finish in virtual-time
// order, b at 15 ms and a at 35 ms; c is cancelled at 20 ms, as a client
// disconnecting would, and its plat.http answers Cancelled at that moment.
// Everything printed is virtual time, so the output is the same bytes on
// every target.
const SimTasksProgram = `import "std/async";
import "std/sim";
import "std/sim_fetch";
import "std/sim_platform";
import "std/platform";
import "std/fetch";

const UP: string = "93.184.216.34";

function fetch_via(plat: platform.Platform, path: string, log: Cell[string]): string {
    match (plat.http(fetch.get("http://up.test" + path))) {
        Ok(resp) => {
            log.set(log.get() + path + " " + resp.body_string() + " at " + plat.now_ms().to_string() + "\n");
            return resp.body_string();
        },
        Err(e) => {
            log.set(log.get() + path + " " + e.message() + " at " + plat.now_ms().to_string() + "\n");
            return "none";
        }
    }
    return "";
}

function main(): i32 {
    let d: sim.Sim = sim.new(1);
    let n: sim_fetch.Net = sim_fetch.net(d).host("up.test", [UP]).listen(UP, 80, 5)
        .route(UP, 80, "/a", [sim_fetch.reply(200, "alpha").after(30)])
        .route(UP, 80, "/b", [sim_fetch.reply(200, "beta").after(10)])
        .route(UP, 80, "/c", [sim_fetch.reply(200, "gamma").after(50)]);
    let plat: sim_platform.SimPlatform = sim_platform.new(d, n);
    let log: Cell[string] = cell_new("");
    let entries: (() => string)[] = [
        () => fetch_via(plat, "/a", log),
        () => fetch_via(plat, "/b", log),
        () => fetch_via(plat, "/c", log)
    ];
    let got: async.TaskStatus[string][] = sim.run_tasks(d, entries, [0 - 1, 0 - 1, 20]);
    print(log.get().trim());
    for st in got {
        match (st) {
            Done(v) => { print("done " + v); },
            Suspended(w) => { print("suspended"); },
            Cancelled => { print("cancelled"); }
        }
    }
    print("now " + (d.now_ns() / 1000000).to_string());
    return 0;
}
`

// SimTasksWant is the self-host output: the tasks park, so they finish in
// virtual-time order and the third is cancelled while it waits.
const SimTasksWant = "/b beta at 15\n/c cancelled at 20\n/a alpha at 35\ndone alpha\ndone beta\ncancelled\nnow 35\n"
