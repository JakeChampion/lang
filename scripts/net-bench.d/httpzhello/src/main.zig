//! The http.zig hello server scripts/net-bench measures: the same static
//! body as the Fern, Go and hyper servers on PORT, http.zig at the commit
//! build.zig.zon pins (its zig-0.16.0 branch), one I/O worker thread and
//! one handler thread per core, with the connection cap lifted as the
//! Fern server lifts its own.
const std = @import("std");
const httpz = @import("httpz");

pub fn main(init: std.process.Init) !void {
    var port: u16 = 8080;
    if (init.environ_map.get("PORT")) |p| {
        port = try std.fmt.parseInt(u16, p, 10);
    }
    const cores: u16 = @intCast(try std.Thread.getCpuCount());
    var server = try httpz.Server(void).init(init.io, init.gpa, .{
        .address = .localhost(port),
        .workers = .{ .count = cores, .max_conn = 16384 },
        .thread_pool = .{ .count = cores },
    }, {});
    defer server.deinit();
    var router = try server.router(.{});
    router.get("/", hello, .{});
    try server.listen();
}

fn hello(_: *httpz.Request, res: *httpz.Response) !void {
    res.body = "hello";
}
