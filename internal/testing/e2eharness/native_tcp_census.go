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
	read := BytesOf + `function read(c: i32, buf: u8[]): i32 {
    let n: i32 = tcp_recv_into(c, buf);
    let i: i32 = 0;
    while (i < n) {
        if (buf[i] != 120u8) { return 0 - 1000; }
        i = i + 1;
    }
    return n;
}
`
	for _, n := range []int{0, 1, 7, 4097} {
		src := read + fmt.Sprintf(`function exercise(): i32 {
    let listener: i32 = tcp_listen(0);
    if (listener < 0) { return 1; }
    let port: i32 = tcp_local_port(listener);
    if (port <= 0) { return 2; }
    let client: i32 = tcp_connect_with([127u8, 0u8, 0u8, 1u8], port, false);
    if (client < 0) { return 3; }
    let server: i32 = tcp_accept(listener);
    if (server < 0) { return 4; }
    if (tcp_send_bytes(client, bytes_of(%q)) != %d) { return 5; }
    if (tcp_close(client) != 0) { return 6; }
    let buf: u8[] = __alloc_u8(4096);
    let total: i32 = 0;
    let n: i32 = read(server, buf);
    while (n > 0) { total = total + n; n = read(server, buf); }
    if (n < 0 || total != %d) { return 7; }
    if (tcp_close(server) != 0 || tcp_close(listener) != 0) { return 8; }
    return 0;
}
function main(): i32 {
    let i: i32 = 0;
    while (i < 32) {
        let result: i32 = exercise();
        if (result != 0) { return result; }
        i = i + 1;
    }
    return 0;
}
`, strings.Repeat("x", n), n, n)
		cases = append(cases, struct{ Name, Source string }{fmt.Sprintf("bytes%d", n), src})
	}
	cases = append(cases, struct{ Name, Source string }{"invalid-fd", read + `function main(): i32 {
    let i: i32 = 0;
    while (i < 32) {
        let buf: u8[] = __alloc_u8(4096);
        if (read(-1, buf) >= 0) { return 1; }
        i = i + 1;
    }
    return 0;
}
`})
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
