const std = @import("std");

pub fn build(b: *std.Build) void {
    const target = b.standardTargetOptions(.{});
    const optimize = b.standardOptimizeOption(.{});
    const zap = b.dependency("zap", .{ .target = target, .optimize = optimize, .openssl = false });
    const exe = b.addExecutable(.{
        .name = "zap-hello",
        .root_module = b.createModule(.{
            .root_source_file = b.path("src/main.zig"),
            .target = target,
            .optimize = optimize,
            .imports = &.{.{ .name = "zap", .module = zap.module("zap") }},
        }),
    });
    b.installArtifact(exe);
}
