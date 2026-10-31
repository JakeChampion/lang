package e2eharness

const SimTasksProgram = `import "std/time";
import "std/async";
import "std/sim";
import "std/sim_fetch";
import "std/sim_platform";
import "std/platform";
import "std/fetch";
import "std/http";
import "std/option";

const UP: string = "93.184.216.34";

function fetch_via(plat: platform.Platform, path: string, log: Cell[string]): string {
    match (plat.http(fetch.get("http://up.test" + path))) {
        Ok(resp) => {
            log.set(log.get() + path + " " + resp.body_string() + " at " + plat.now().unix_millis().to_string() + "\n");
            return resp.body_string();
        },
        Err(e) => {
            log.set(log.get() + path + " " + e.message() + " at " + plat.now().unix_millis().to_string() + "\n");
            return "none";
        }
    }
    return "";
}

function body_via(plat: platform.Platform, path: string): string {
    match (plat.http(fetch.get("http://up.test" + path))) {
        Ok(resp) => { return resp.body_string(); },
        Err(e) => { return e.message(); }
    }
    return "";
}

// fan_out is a handler (#12456). It fetches /a and /b at once, and then /c
// under a 20ms deadline. Its clock and its parks are the simulation's.
function fan_out(req: HttpRequest, plat: platform.Platform): HttpResponse {
    let both: async.Future[string][] = [async.spawn((): string => body_via(plat, "/a")), async.spawn((): string => body_via(plat, "/b"))];
    let got: string = "cancelled";
    match (async.wait(async.gather(both))) {
        Some(bodies) => { got = bodies[0] + "+" + bodies[1]; },
        None => {}
    }
    let late: async.Future[string][] = [async.spawn((): string => body_via(plat, "/c"))];
    let timed: string = "cancelled";
    match (async.wait(async.with_deadline(plat.instant().add(time.duration_millis(20)), late))) {
        Some(slots) => { timed = slots[0].unwrap_or("late"); },
        None => {}
    }
    return http.ok(got + " " + timed + " at " + plat.now().unix_millis().to_string());
}

function main(): i32 {
    let d: sim.Sim = sim.new(1);
    let n: sim_fetch.Net = sim_fetch.new(d).host("up.test", [UP]).listen(UP, 80, time.duration_millis(5))
        .route(UP, 80, "/a", [sim_fetch.reply(200, "alpha").after(time.duration_millis(30))])
        .route(UP, 80, "/b", [sim_fetch.reply(200, "beta").after(time.duration_millis(10))])
        .route(UP, 80, "/c", [sim_fetch.reply(200, "gamma").after(time.duration_millis(50))]);
    let plat: sim_platform.SimPlatform = sim_platform.new(d, n);
    let log: Cell[string] = cell_new("");
    let entries: (() => string)[] = [
        () => fetch_via(plat, "/a", log),
        () => fetch_via(plat, "/b", log),
        () => fetch_via(plat, "/c", log)
    ];
    let got: async.TaskStatus[string][] = sim.run_tasks(d, entries, [None, None, Some(sim.at(time.duration_millis(20)))]);
    print(log.get().trim());
    for st in got {
        match (st) {
            Done(v) => { print("done " + v); },
            Suspended(w) => { print("suspended"); }
        }
    }
    print("now " + (d.elapsed().as_nanos() / 1000000).to_string());
    let h: sim.Sim = sim.new(2);
    let hn: sim_fetch.Net = sim_fetch.new(h).host("up.test", [UP]).listen(UP, 80, time.duration_millis(5))
        .route(UP, 80, "/a", [sim_fetch.reply(200, "alpha").after(time.duration_millis(30))])
        .route(UP, 80, "/b", [sim_fetch.reply(200, "beta").after(time.duration_millis(10))])
        .route(UP, 80, "/c", [sim_fetch.reply(200, "gamma").after(time.duration_millis(50))]);
    let hp: sim_platform.SimPlatform = sim_platform.new(h, hn);
    let handled: (() => string)[] = [() => fan_out(http.request("GET", "/"), hp).body_string()];
    for st in sim.run_tasks(h, handled, [None]) {
        match (st) {
            Done(v) => { print("handler " + v); },
            Suspended(w) => { print("handler suspended"); }
        }
    }
    print("now " + (h.elapsed().as_nanos() / 1000000).to_string());
    return 0;
}
`

const SimTasksWant = "/b beta at 15\n/c cancelled at 20\n/a alpha at 35\ndone alpha\ndone beta\ndone none\nnow 35\n" +
	"handler alpha+beta late at 55\nnow 55\n"
