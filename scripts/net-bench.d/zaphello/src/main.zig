//! The zap hello server scripts/net-bench measures: the same static body
//! as the Fern, Go and hyper servers on PORT, zap at the tag build.zig.zon
//! pins over its vendored facil.io, one worker process per core with one
//! thread each (facil.io's reactor per process, the shape the Fern server
//! forks), and the client cap lifted as the Fern server lifts its own.
const std = @import("std");
const zap = @import("zap");

fn hello(r: zap.Request) !void {
    try r.sendBody("hello");
}

pub fn main(init: std.process.Init) !void {
    var port: usize = 8080;
    if (init.environ_map.get("PORT")) |p| {
        port = try std.fmt.parseInt(usize, p, 10);
    }
    var listener = zap.HttpListener.init(.{
        .port = port,
        .interface = "127.0.0.1",
        .on_request = hello,
        .log = false,
        .max_clients = 100000,
    });
    try listener.listen();
    const cores: i16 = @intCast(try std.Thread.getCpuCount());
    zap.start(.{ .threads = 1, .workers = cores });
}
