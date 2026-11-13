package e2ecompiler

import (
	"bufio"
	"bytes"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
)

func TestSelfHostExampleLoadUnit(t *testing.T) {
	path, err := filepath.Abs("../../../examples/load/load_test.fern")
	if err != nil {
		t.Fatal(err)
	}
	testCensusTargets(t, path)
}

// loadServer counts the requests and the connections it gets.
type loadServer struct {
	*httptest.Server
	requests    atomic.Int64
	connections atomic.Int64
	mismatches  atomic.Int64
}

func newLoadServer(t *testing.T) *loadServer {
	t.Helper()
	s := &loadServer{}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		s.requests.Add(1)
		_, _ = w.Write([]byte("hello\n"))
	})
	mux.HandleFunc("/chunked", func(w http.ResponseWriter, r *http.Request) {
		s.requests.Add(1)
		_, _ = w.Write([]byte("part one,"))
		w.(http.Flusher).Flush()
		_, _ = w.Write([]byte("part two\n"))
	})
	// A body that takes many reads.
	mux.HandleFunc("/large", func(w http.ResponseWriter, r *http.Request) {
		s.requests.Add(1)
		_, _ = w.Write(bytes.Repeat([]byte("0123456789abcdef"), 20000))
	})
	mux.HandleFunc("/echo", func(w http.ResponseWriter, r *http.Request) {
		s.requests.Add(1)
		body, _ := io.ReadAll(r.Body)
		if r.Method != "POST" || string(body) != "payload" || r.Header.Get("X-Load") != "yes" || r.ContentLength != 7 {
			s.mismatches.Add(1)
		}
		w.WriteHeader(http.StatusCreated)
	})
	// A body that ends when the server closes the connection.
	mux.HandleFunc("/close", func(w http.ResponseWriter, r *http.Request) {
		s.requests.Add(1)
		hijack(t, w, "HTTP/1.1 200 OK\r\n\r\nto the end")
	})
	// A response that stops before its Content-Length.
	mux.HandleFunc("/cut", func(w http.ResponseWriter, r *http.Request) {
		s.requests.Add(1)
		hijack(t, w, "HTTP/1.1 200 OK\r\nContent-Length: 10\r\n\r\nabc")
	})
	s.Server = httptest.NewUnstartedServer(mux)
	s.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			s.connections.Add(1)
		}
	}
	s.Start()
	t.Cleanup(s.Close)
	return s
}

func hijack(t *testing.T, w http.ResponseWriter, raw string) {
	conn, buf, err := w.(http.Hijacker).Hijack()
	if err != nil {
		t.Error(err)
		return
	}
	_, _ = buf.WriteString(raw)
	_ = buf.Flush()
	_ = conn.Close()
}

// loadSummary is what the tool reports.
type loadSummary struct {
	text     string
	statuses map[int]int
	errors   map[string]int
	total    float64
}

var (
	loadStatusRe = regexp.MustCompile(`^  \[(\d{3})\] (\d+) responses$`)
	loadErrorRe  = regexp.MustCompile(`^  \[(\d+)\] (.+)$`)
	loadTotalRe  = regexp.MustCompile(`^  Total:\t(\d+\.\d{4}) (ns|us|ms|secs)$`)
	loadUnitNs   = map[string]float64{"ns": 1, "us": 1e3, "ms": 1e6, "secs": 1e9}
)

// parseLoadSummary checks the layout of the summary and reads its counts.
func parseLoadSummary(t *testing.T, out string) loadSummary {
	t.Helper()
	s := loadSummary{text: out, statuses: map[int]int{}, errors: map[string]int{}}
	var sections []string
	section := ""
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		line := sc.Text()
		switch {
		case line == "":
		case !strings.HasPrefix(line, "  "):
			section = line
			sections = append(sections, line)
		case section == "Status code distribution:":
			m := loadStatusRe.FindStringSubmatch(line)
			if m == nil {
				t.Fatalf("status row %q\n%s", line, out)
			}
			code, _ := strconv.Atoi(m[1])
			n, _ := strconv.Atoi(m[2])
			s.statuses[code] = n
		case section == "Error distribution:":
			m := loadErrorRe.FindStringSubmatch(line)
			if m == nil {
				t.Fatalf("error row %q\n%s", line, out)
			}
			n, _ := strconv.Atoi(m[1])
			s.errors[m[2]] = n
		case section == "Summary:":
			if m := loadTotalRe.FindStringSubmatch(line); m != nil {
				v, _ := strconv.ParseFloat(m[1], 64)
				s.total = v * loadUnitNs[m[2]]
			}
		}
	}
	want := []string{"Summary:", "Response time distribution:", "Status code distribution:"}
	if len(s.statuses) == 0 {
		want = []string{"Summary:", "Status code distribution:"}
	}
	if len(s.errors) > 0 {
		want = append(want, "Error distribution:")
	}
	if strings.Join(sections, "|") != strings.Join(want, "|") {
		t.Fatalf("sections %q, want %q\n%s", sections, want, out)
	}
	if s.total == 0 {
		t.Fatalf("no Total row\n%s", out)
	}
	for _, row := range []string{"Success rate:", "Requests/sec:"} {
		if !strings.Contains(out, "\n  "+row+"\t") {
			t.Fatalf("no %s row\n%s", row, out)
		}
	}
	if len(s.statuses) > 0 {
		for _, p := range []string{"10.00", "25.00", "50.00", "75.00", "90.00", "95.00", "99.00", "99.90", "99.99"} {
			if !strings.Contains(out, "\n  "+p+"% in ") {
				t.Fatalf("no %s%% row\n%s", p, out)
			}
		}
	}
	return s
}

func TestSelfHostExampleLoadHTTP(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src, err := filepath.Abs("../../../examples/load/load.fern")
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		t.Run(target, func(t *testing.T) {
			var load func(args ...string) *exec.Cmd
			if target == "x86-64-linux" {
				bin := cli.x86Binary(t, src)
				load = func(args ...string) *exec.Cmd { return runX86_64Bin(cli.runner, bin, args...) }
			} else {
				_, qemu := arm64Tooling(t)
				bin := cli.arm64Binary(t, src)
				load = func(args ...string) *exec.Cmd { return runArm64Bin(qemu, bin, args...) }
			}
			run := func(t *testing.T, args ...string) (string, string, int) {
				t.Helper()
				cmd := load(args...)
				var stdout, stderr bytes.Buffer
				cmd.Stdout, cmd.Stderr = &stdout, &stderr
				_ = cmd.Run()
				if cmd.ProcessState == nil {
					t.Fatalf("load did not start: %s", &stderr)
				}
				return stdout.String(), stderr.String(), cmd.ProcessState.ExitCode()
			}
			summary := func(t *testing.T, args ...string) loadSummary {
				t.Helper()
				out, stderr, code := run(t, args...)
				if code != 0 {
					t.Fatalf("load %v: exit %d\n%s%s", args, code, out, stderr)
				}
				return parseLoadSummary(t, out)
			}

			t.Run("count", func(t *testing.T) {
				s := newLoadServer(t)
				got := summary(t, "-n", "500", "-c", "8", s.URL+"/")
				if got.statuses[200] != 500 || len(got.statuses) != 1 || len(got.errors) != 0 {
					t.Errorf("statuses %v, errors %v; want 500 responses of 200\n%s", got.statuses, got.errors, got.text)
				}
				if n := s.requests.Load(); n != 500 {
					t.Errorf("the server got %d requests, want 500", n)
				}
				if n := s.connections.Load(); n != 8 {
					t.Errorf("the server got %d connections, want 8: one for each of -c 8", n)
				}
				if !strings.Contains(got.text, "  Success rate:\t100.00%\n") {
					t.Errorf("success rate is not 100%%\n%s", got.text)
				}
			})

			t.Run("chunked", func(t *testing.T) {
				s := newLoadServer(t)
				got := summary(t, "-n", "300", "-c", "5", s.URL+"/chunked")
				if got.statuses[200] != 300 || len(got.errors) != 0 || s.requests.Load() != 300 || s.connections.Load() != 5 {
					t.Errorf("statuses %v, errors %v, server %d requests on %d connections; want 300 on 5\n%s", got.statuses, got.errors, s.requests.Load(), s.connections.Load(), got.text)
				}
			})

			t.Run("large", func(t *testing.T) {
				s := newLoadServer(t)
				got := summary(t, "-n", "40", "-c", "3", s.URL+"/large")
				if got.statuses[200] != 40 || len(got.errors) != 0 || s.requests.Load() != 40 || s.connections.Load() != 3 {
					t.Errorf("statuses %v, errors %v, server %d requests on %d connections; want 40 on 3\n%s", got.statuses, got.errors, s.requests.Load(), s.connections.Load(), got.text)
				}
			})

			t.Run("duration", func(t *testing.T) {
				s := newLoadServer(t)
				got := summary(t, "-z", "1s", "-c", "4", s.URL+"/")
				done, aborted := got.statuses[200], got.errors["aborted due to deadline"]
				if done == 0 || aborted > 4 || len(got.errors) > 1 || len(got.errors) == 1 && aborted == 0 {
					t.Errorf("statuses %v, errors %v\n%s", got.statuses, got.errors, got.text)
				}
				// An aborted request can reach the server or not.
				if n := s.requests.Load(); n < int64(done) || n > int64(done+aborted) {
					t.Errorf("the server got %d requests, want %d to %d", n, done, done+aborted)
				}
				if got.total < 1e9 || got.total > 2e9 {
					t.Errorf("Total is %.0f ns, want about 1 s\n%s", got.total, got.text)
				}
			})

			t.Run("duration wait", func(t *testing.T) {
				s := newLoadServer(t)
				got := summary(t, "-z", "500ms", "-w", "-c", "4", s.URL+"/chunked")
				if done := got.statuses[200]; done == 0 || len(got.errors) != 0 || s.requests.Load() != int64(done) {
					t.Errorf("statuses %v, errors %v, server %d requests; want every request complete\n%s", got.statuses, got.errors, s.requests.Load(), got.text)
				}
			})

			t.Run("method header body", func(t *testing.T) {
				s := newLoadServer(t)
				got := summary(t, "-n", "20", "-c", "2", "-m", "POST", "-d", "payload", "-H", "X-Load: yes", s.URL+"/echo")
				if got.statuses[201] != 20 || s.requests.Load() != 20 || s.mismatches.Load() != 0 {
					t.Errorf("statuses %v, server %d requests, %d not as sent\n%s", got.statuses, s.requests.Load(), s.mismatches.Load(), got.text)
				}
			})

			t.Run("close delimited", func(t *testing.T) {
				s := newLoadServer(t)
				got := summary(t, "-n", "10", "-c", "2", s.URL+"/close")
				if got.statuses[200] != 10 || len(got.errors) != 0 || s.requests.Load() != 10 || s.connections.Load() != 10 {
					t.Errorf("statuses %v, errors %v, server %d requests on %d connections; want 10 on 10\n%s", got.statuses, got.errors, s.requests.Load(), s.connections.Load(), got.text)
				}
			})

			t.Run("incomplete", func(t *testing.T) {
				s := newLoadServer(t)
				got := summary(t, "-n", "6", "-c", "2", s.URL+"/cut")
				if len(got.statuses) != 0 || got.errors["connection closed before the response was complete"] != 6 || s.requests.Load() != 6 {
					t.Errorf("statuses %v, errors %v; want 6 incomplete responses\n%s", got.statuses, got.errors, got.text)
				}
				if !strings.Contains(got.text, "  Success rate:\t0.00%\n") {
					t.Errorf("success rate is not 0%%\n%s", got.text)
				}
			})

			t.Run("refused", func(t *testing.T) {
				l, err := net.Listen("tcp", "127.0.0.1:0")
				if err != nil {
					t.Fatal(err)
				}
				addr := l.Addr().String()
				l.Close()
				got := summary(t, "-n", "5", "-c", "2", "http://"+addr+"/")
				if got.errors["Connection refused"] != 5 || len(got.statuses) != 0 {
					t.Errorf("errors %v; want 5 refused connections\n%s", got.errors, got.text)
				}
			})

			t.Run("usage", func(t *testing.T) {
				for _, tc := range []struct {
					args []string
					want string
				}{
					{[]string{"https://127.0.0.1/"}, "load: https URLs are not supported yet: use an http URL\n"},
					{[]string{"-z", "10", "http://127.0.0.1/"}, "load: -z 10 is not a duration such as 10s, 500ms or 1m\n"},
					{[]string{}, "load: give one URL\n"},
				} {
					out, stderr, code := run(t, tc.args...)
					if code != 2 || out != "" || !strings.HasPrefix(stderr, tc.want) || !strings.Contains(stderr, "Usage: load ") {
						t.Errorf("load %v: exit %d, stdout %q, stderr %q; want exit 2 and %q", tc.args, code, out, stderr, tc.want)
					}
				}
				if out, _, code := run(t, "--help"); code != 0 || !strings.HasPrefix(out, "Usage: load ") {
					t.Errorf("load --help: exit %d, stdout %q", code, out)
				}
			})
		})
	}
}
