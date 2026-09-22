package e2eharness

import (
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

func NativeTCPCensusCases() []struct{ Name, Source string } {
	cases := []struct{ Name, Source string }{}
	read := `function read(c: i32, limit: i32): i32 {
    var bytes: u8[] = tcp_recv(c, limit);
    var i: i32 = 0;
    while (i < bytes.len()) {
        if (bytes[i] != 120) { return -1; }
        i = i + 1;
    }
    return bytes.len();
}
`
	for _, n := range []int{0, 1, 7, 4097} {
		src := read + fmt.Sprintf(`function exercise(): i32 {
    var listener: i32 = tcp_listen(0);
    if (listener < 0) { return 1; }
    var port: i32 = tcp_local_port(listener);
    if (port <= 0) { return 2; }
    var client: i32 = tcp_connect(127 + 16777216, port);
    if (client < 0) { return 3; }
    var server: i32 = tcp_accept(listener);
    if (server < 0) { return 4; }
    if (tcp_send(client, %q) != %d) { return 5; }
    if (tcp_close(client) != 0) { return 6; }
    var total: i32 = 0;
    var n: i32 = read(server, 4096);
    while (n > 0) { total = total + n; n = read(server, 4096); }
    if (n < 0 || total != %d) { return 7; }
    if (tcp_close(server) != 0 || tcp_close(listener) != 0) { return 8; }
    return 0;
}
function main(): i32 {
    var i: i32 = 0;
    while (i < 32) {
        var result: i32 = exercise();
        if (result != 0) { return result; }
        i = i + 1;
    }
    return 0;
}
`, strings.Repeat("x", n), n, n)
		cases = append(cases, struct{ Name, Source string }{fmt.Sprintf("bytes%d", n), src})
	}
	for _, limit := range []int{4096, 0, -1} {
		src := read + fmt.Sprintf(`function main(): i32 {
    var i: i32 = 0;
    while (i < 32) {
        if (read(-1, %d) != 0) { return 1; }
        i = i + 1;
    }
    return 0;
}
`, limit)
		cases = append(cases, struct{ Name, Source string }{fmt.Sprintf("invalid-fd-limit%d", limit), src})
	}
	return cases
}

func CheckNativeTCPCensus(t *testing.T, command *exec.Cmd) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, command.Path, command.Args[1:]...)
	cmd.Env, cmd.Dir = command.Env, command.Dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("TCP lifecycle: %v\n%s", err, out)
	}
	matches := regexp.MustCompile(`(?m)^leakcheck: allocs=([0-9]+) frees=([0-9]+) live_bytes=([0-9]+)$`).FindAllStringSubmatch(string(out), -1)
	if len(matches) != 1 {
		t.Fatalf("want exactly one allocation census: %s", out)
	}
	counts := [3]int64{}
	for i := range counts {
		n, err := strconv.ParseInt(matches[0][i+1], 10, 64)
		if err != nil {
			t.Fatal(err)
		}
		counts[i] = n
	}
	t.Log(matches[0][0])
	if counts[0] != counts[1] || counts[2] != 0 {
		t.Fatalf("TCP lifecycle must reclaim every allocation: %s", matches[0][0])
	}
}
