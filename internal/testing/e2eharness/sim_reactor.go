package e2eharness

func SimReactorProbe() string {
	return `import "core/int";
import "std/async";
import "std/sim";

function fail(n: i32): i32 {
    print(int.int_to_string(n));
    return n;
}

function main(): i32 {
    let drv: sim.Sim = sim.new(7 as i64);
    if (drv.watch(7, 1) != 0) { return fail(1); }
    if (drv.wait(2, 20).len() != 0) { return fail(2); }
    if (drv.now_ns() != (20000000 as i64)) { return fail(3); }
    drv.ready_at(7, 50, 1);
    drv.ready_at(9, 30, 1);
    if (drv.wait(2, 5).len() != 0) { return fail(4); }
    if (drv.now_ns() != (25000000 as i64)) { return fail(5); }
    let got: i32[] = drv.wait(2, -1);
    if (got.len() != 2 || got[0] != 7 || got[1] != 1) { return fail(6); }
    if (drv.now_ns() != (50000000 as i64)) { return fail(7); }
    if (drv.wait(2, 10).len() != 0) { return fail(8); }
    if (drv.watch(9, 2) != 0) { return fail(9); }
    if (drv.wait(2, 10).len() != 0) { return fail(10); }
    if (drv.watch(9, 3) != 0) { return fail(11); }
    got = drv.wait(2, 10);
    if (got.len() != 2 || got[0] != 9 || got[1] != 1) { return fail(12); }
    if (drv.now_ns() != (70000000 as i64)) { return fail(13); }
    drv.ready_at(9, 80, 2);
    if (drv.unwatch(9) != 0) { return fail(14); }
    if (drv.wait(2, 30).len() != 0) { return fail(15); }
    if (drv.wait(0, 0).len() != 0) { return fail(16); }
    if (drv.wait(2, -1).len() != 0) { return fail(17); }
    let events: i32[] = async.event_room(2);
    if (async.ready_pairs(events) != 0) { return fail(18); }
    if (drv.watch(8, 1) != 0) { return fail(19); }
    drv.ready_at(7, 120, 1);
    drv.ready_at(8, 120, 1);
    drv.ready_at(7, 140, 1);
    events = drv.wait_into(events, -1);
    if (async.ready_pairs(events) != 2 || events[0] != 7 || events[2] != 8) { return fail(20); }
    events = drv.wait_into(events, -1);
    if (events.len() != 4 || async.ready_pairs(events) != 1 || events[0] != 7) { return fail(21); }
    events = drv.wait_into(events, 10);
    if (events.len() != 4 || async.ready_pairs(events) != 0) { return fail(22); }
    if (async.ready_pairs(async.end_pairs([5, 1, 6, 1], 1)) != 1) { return fail(23); }
    if (drv.close() != 0) { return fail(24); }
    print("ok");
    return 42;
}
`
}
