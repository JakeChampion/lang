// CI-DARK: FERN_SERVE_ALLOC_CENSUS — This flag reports allocation measurements.
package e2ecompiler

import (
	"bufio"
	"debug/elf"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

const (
	censusShort = 1000
	censusLong  = 3000
)

type censusSite struct{ site, caller string }

func TestSelfHostServeAllocsCensusH3(t *testing.T) {
	out := os.Getenv("FERN_SERVE_ALLOC_CENSUS")
	if out == "" {
		t.Skip("set FERN_SERVE_ALLOC_CENSUS to a file to write the census to")
	}
	cli := buildSelfHostCLI(t)
	var report strings.Builder
	for _, mux := range []bool{false, true} {
		short := censusRun(t, cli, mux, censusShort)
		long := censusRun(t, cli, mux, censusLong)
		name := "serial"
		if mux {
			name = fmt.Sprintf("multiplexed, %d in flight", ServeAllocsH3MuxConcurrency)
		}
		writeCensus(&report, name, short, long, censusLong-censusShort)
	}
	if err := os.WriteFile(out, []byte(report.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}

func censusRun(t *testing.T, cli *selfHostCLI, mux bool, rounds int) map[censusSite]int {
	t.Helper()
	dir := t.TempDir()
	src := filepath.Join(dir, "main.fern")
	if err := os.WriteFile(src, []byte(e2eharness.ServeAllocsH3ServerSource(0, rounds)), 0o644); err != nil {
		t.Fatal(err)
	}
	clientText := e2eharness.ServeAllocsH3ClientSource
	if mux {
		clientText = e2eharness.ServeAllocsH3MuxClientSource
	}
	clientSrc := filepath.Join(dir, "client.fern")
	if err := os.WriteFile(clientSrc, []byte(clientText), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := cli.x86Binary(t, src, "FERN_STRICT_IR=1", "FERN_RC_TRACE=1")
	client := cli.x86Binary(t, clientSrc, "FERN_STRICT_IR=1")
	addr, stderrPath := e2eharness.StartInheritedServer(t, runX86_64Bin(cli.runner, bin))
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatal(err)
	}
	e2eharness.WaitServerReady(t, addr, 10*time.Second)
	args := []string{port, strconv.Itoa(rounds)}
	if mux {
		args = append(args, strconv.Itoa(ServeAllocsH3MuxConcurrency))
	}
	if msg, err := runX86_64Bin(cli.runner, client, args...).CombinedOutput(); err != nil {
		t.Fatalf("h3 client: %v\n%s", err, msg)
	}
	return traceSites(t, bin, stderrPath)
}

func traceSites(t *testing.T, bin, trace string) map[censusSite]int {
	t.Helper()
	syms := textSymbols(t, bin)
	name := func(hex string) string {
		addr, err := strconv.ParseUint(hex, 16, 64)
		if err != nil {
			return "?"
		}
		i := sort.Search(len(syms), func(i int) bool { return syms[i].Value > addr }) - 1
		if i < 0 {
			return "?"
		}
		return strings.TrimSuffix(strings.TrimPrefix(syms[i].Name, "__fn_"), ".r")
	}
	f, err := os.Open(trace)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	counts := map[censusSite]int{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) == 6 && fields[0] == "rctrace" && fields[1] == "a" {
			counts[censusSite{name(fields[4]), name(fields[5])}]++
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return counts
}

func textSymbols(t *testing.T, bin string) []elf.Symbol {
	t.Helper()
	f, err := elf.Open(bin)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	text := f.Section(".text")
	all, err := f.Symbols()
	if err != nil {
		t.Fatal(err)
	}
	var syms []elf.Symbol
	for _, s := range all {
		if text != nil && s.Value >= text.Addr && s.Value < text.Addr+text.Size && s.Name != "" {
			syms = append(syms, s)
		}
	}
	sort.Slice(syms, func(i, j int) bool { return syms[i].Value < syms[j].Value })
	return syms
}

func writeCensus(w *strings.Builder, name string, short, long map[censusSite]int, requests int) {
	type row struct {
		k censusSite
		v float64
	}
	var rows []row
	total := 0.0
	modules := map[string]float64{}
	for k := range union(short, long) {
		v := float64(long[k]-short[k]) / float64(requests)
		if v < 0.05 && v > -0.05 {
			continue
		}
		rows = append(rows, row{k, v})
		total += v
		modules[censusModule(k)] += v
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].v != rows[j].v {
			return rows[i].v > rows[j].v
		}
		return rows[i].k.site+rows[i].k.caller < rows[j].k.site+rows[j].k.caller
	})
	fmt.Fprintf(w, "## %s: %.2f allocations per request\n\n", name, total)
	var mods []string
	for m := range modules {
		mods = append(mods, m)
	}
	sort.Slice(mods, func(i, j int) bool { return modules[mods[i]] > modules[mods[j]] })
	for _, m := range mods {
		fmt.Fprintf(w, "%7.2f  %s\n", modules[m], m)
	}
	fmt.Fprintln(w)
	for _, r := range rows {
		fmt.Fprintf(w, "%7.2f  %-50s %s\n", r.v, r.k.site, r.k.caller)
	}
	fmt.Fprintln(w)
}

func union(a, b map[censusSite]int) map[censusSite]bool {
	out := map[censusSite]bool{}
	for k := range a {
		out[k] = true
	}
	for k := range b {
		out[k] = true
	}
	return out
}

func censusModule(k censusSite) string {
	who := k.site
	if strings.HasPrefix(who, "__") {
		who = k.caller
	}
	if i := strings.Index(who, "__"); i > 0 {
		return who[:i]
	}
	return who
}
