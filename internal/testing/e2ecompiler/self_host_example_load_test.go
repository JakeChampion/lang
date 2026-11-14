package e2ecompiler

import (
	"bufio"
	"bytes"
	"encoding/json"
	"encoding/pem"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
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
	// The Host field of the last request.
	host atomic.Value
}

func newLoadServer(t *testing.T) *loadServer {
	t.Helper()
	return startLoadServer(t, false)
}

// startLoadServer starts the server of the tests, with TLS when tls is true.
func startLoadServer(t *testing.T, tls bool) *loadServer {
	t.Helper()
	s := &loadServer{}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		s.requests.Add(1)
		s.host.Store(r.Host)
		_, _ = w.Write([]byte("hello\n"))
	})
	mux.HandleFunc("/sleep", func(w http.ResponseWriter, r *http.Request) {
		s.requests.Add(1)
		time.Sleep(time.Second)
	})
	// A redirect to "/", and one to itself.
	mux.HandleFunc("/redirect", func(w http.ResponseWriter, r *http.Request) {
		s.requests.Add(1)
		http.Redirect(w, r, "/", http.StatusFound)
	})
	mux.HandleFunc("/loop", func(w http.ResponseWriter, r *http.Request) {
		s.requests.Add(1)
		http.Redirect(w, r, "loop", http.StatusFound)
	})
	// The fields of -A, -T, -a, --host and --disable-keepalive.
	mux.HandleFunc("/fields", func(w http.ResponseWriter, r *http.Request) {
		s.requests.Add(1)
		user, pass, ok := r.BasicAuth()
		if r.Header.Get("Accept") != "text/x-load" || r.Header.Get("Content-Type") != "application/x-load" || !ok || user != "user" || pass != "secret" || r.Host != "virtual.test" || !r.Close {
			s.mismatches.Add(1)
		}
	})
	// Every 20th request waits for loadStall.
	mux.HandleFunc("/stall", func(w http.ResponseWriter, r *http.Request) {
		if s.requests.Add(1)%20 == 0 {
			time.Sleep(loadStall)
		}
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
	if tls {
		s.StartTLS()
	} else {
		s.Start()
	}
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

const loadStall = 50 * time.Millisecond

// loadTriple is an object of `details`.
type loadTriple struct {
	Average *float64 `json:"average"`
	Fastest *float64 `json:"fastest"`
	Slowest *float64 `json:"slowest"`
}

// loadJSON is the JSON summary, with the keys of oha 1.16.
type loadJSON struct {
	Summary struct {
		SuccessRate    float64  `json:"successRate"`
		Total          float64  `json:"total"`
		Slowest        *float64 `json:"slowest"`
		Fastest        *float64 `json:"fastest"`
		Average        *float64 `json:"average"`
		RequestsPerSec float64  `json:"requestsPerSec"`
		TotalData      int64    `json:"totalData"`
		SizePerRequest *int64   `json:"sizePerRequest"`
		SizePerSec     float64  `json:"sizePerSec"`
	} `json:"summary"`
	Metrics struct {
		LatencyMs map[string]*float64 `json:"latency_ms"`
	} `json:"metrics"`
	ResponseTimeHistogram map[string]int      `json:"responseTimeHistogram"`
	LatencyPercentiles    map[string]*float64 `json:"latencyPercentiles"`
	FirstByteHistogram    map[string]int      `json:"firstByteHistogram"`
	FirstBytePercentiles  map[string]*float64 `json:"firstBytePercentiles"`
	Rps                   struct {
		Mean        *float64            `json:"mean"`
		Stddev      *float64            `json:"stddev"`
		Max         *float64            `json:"max"`
		Min         *float64            `json:"min"`
		Percentiles map[string]*float64 `json:"percentiles"`
	} `json:"rps"`
	Details struct {
		DNSDialup loadTriple `json:"DNSDialup"`
		DNSLookup loadTriple `json:"DNSLookup"`
		FirstByte loadTriple `json:"firstByte"`
	} `json:"details"`
	StatusCodeDistribution map[string]int `json:"statusCodeDistribution"`
	ErrorDistribution      map[string]int `json:"errorDistribution"`
}

// loadJSONKeys are the top-level keys of `oha --output-format json`, in its
// order.
var loadJSONKeys = []string{"summary", "metrics", "responseTimeHistogram", "latencyPercentiles", "firstByteHistogram", "firstBytePercentiles", "rps", "details", "statusCodeDistribution", "errorDistribution"}

// parseLoadJSON checks the top-level keys of the JSON summary and reads it.
func parseLoadJSON(t *testing.T, out string) loadJSON {
	t.Helper()
	dec := json.NewDecoder(strings.NewReader(out))
	var order []string
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		t.Fatalf("not a JSON object: %v\n%s", err, out)
	}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			t.Fatalf("not JSON: %v\n%s", err, out)
		}
		order = append(order, tok.(string))
		var skip json.RawMessage
		if err := dec.Decode(&skip); err != nil {
			t.Fatalf("not JSON: %v\n%s", err, out)
		}
	}
	if strings.Join(order, " ") != strings.Join(loadJSONKeys, " ") {
		t.Fatalf("top-level keys %q, want %q\n%s", order, loadJSONKeys, out)
	}
	var got loadJSON
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("JSON summary: %v\n%s", err, out)
	}
	if len(got.LatencyPercentiles) != 9 || len(got.Metrics.LatencyMs) != 6 || len(got.FirstBytePercentiles) != 9 || len(got.Rps.Percentiles) != 9 {
		t.Fatalf("%d percentiles, %d latency_ms values, %d first byte percentiles and %d rps percentiles, want 9, 6, 9 and 9\n%s", len(got.LatencyPercentiles), len(got.Metrics.LatencyMs), len(got.FirstBytePercentiles), len(got.Rps.Percentiles), out)
	}
	n := got.responses()
	if n > 0 && (len(got.ResponseTimeHistogram) == 0 || len(got.ResponseTimeHistogram) > 11 || sum(got.ResponseTimeHistogram) != n || sum(got.FirstByteHistogram) != n) {
		t.Fatalf("the histograms do not count the %d responses\n%s", n, out)
	}
	if n == 0 && (len(got.ResponseTimeHistogram) != 0 || got.Summary.SizePerRequest != nil || got.Rps.Mean != nil || got.Details.FirstByte.Average != nil) {
		t.Fatalf("a run with no response has histogram rows, a size per request, rps or a first byte\n%s", out)
	}
	if n > 0 && (got.Rps.Mean == nil || got.Details.FirstByte.Average == nil || *got.Details.FirstByte.Fastest > *got.Details.FirstByte.Slowest || *got.Details.FirstByte.Slowest > *got.Summary.Slowest) {
		t.Fatalf("no rps, or first byte times that are not ordered or not before the response\n%s", out)
	}
	return got
}

func sum(m map[string]int) int64 {
	n := 0
	for _, c := range m {
		n += c
	}
	return int64(n)
}

func (j loadJSON) responses() int64 {
	return sum(j.StatusCodeDistribution)
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
	loadTotalRe  = regexp.MustCompile(`^  Total:\t(\d+\.\d{4}) (ns|us|ms|sec|min|hr)$`)
	loadUnitNs   = map[string]float64{"ns": 1, "us": 1e3, "ms": 1e6, "sec": 1e9, "min": 60e9, "hr": 3600e9}
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
	want := []string{"Summary:", "Response time histogram:", "Response time distribution:", "Details (average, fastest, slowest):", "Status code distribution:"}
	if len(s.statuses) == 0 {
		want = []string{"Summary:", "Details (average, fastest, slowest):", "Status code distribution:"}
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
	for _, row := range []string{"Success rate:", "Requests/sec:", "Total data:", "Size/request:", "Size/sec:", "DNS+dialup:", "DNS-lookup:"} {
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
				out, stderr, code := run(t, "-n", "3", "-c", "1", "--output-format", "json", "http://"+addr+"/")
				if code != 0 {
					t.Fatalf("exit %d\n%s%s", code, out, stderr)
				}
				j := parseLoadJSON(t, out)
				if j.ErrorDistribution["Connection refused"] != 3 || j.responses() != 0 || j.Summary.Slowest != nil || j.LatencyPercentiles["p99"] != nil || j.Summary.SuccessRate != 0 {
					t.Errorf("want 3 refused connections and null latencies\n%s", out)
				}
			})

			// With -c 1, a stall delays the requests due after it. Their
			// latency counts from the time each was due, so far more than
			// one request in 20 is slow.
			t.Run("rate json", func(t *testing.T) {
				s := newLoadServer(t)
				const rate, seconds = 200, 1
				out, stderr, code := run(t, "-q", strconv.Itoa(rate), "-z", strconv.Itoa(seconds)+"s", "-c", "1", "-w", "--latency-correction", "--output-format", "json", s.URL+"/stall")
				if code != 0 {
					t.Fatalf("exit %d\n%s%s", code, out, stderr)
				}
				j := parseLoadJSON(t, out)
				n := j.responses()
				if n != s.requests.Load() || len(j.ErrorDistribution) != 0 || j.Summary.SuccessRate != 1 {
					t.Errorf("%d responses, errors %v; the server got %d requests\n%s", n, j.ErrorDistribution, s.requests.Load(), out)
				}
				if n > rate*seconds+1 || n < rate*seconds/2 {
					t.Errorf("%d requests at -q %d for %d s\n%s", n, rate, seconds, out)
				}
				stall := loadStall.Seconds()
				p75, p99 := j.LatencyPercentiles["p75"], j.LatencyPercentiles["p99"]
				if p99 == nil || *p99 < stall || p75 == nil || *p75 < stall/5 {
					t.Errorf("p75 and p99 do not show the %v stalls\n%s", loadStall, out)
				}
				if ms := j.Metrics.LatencyMs["p99"]; ms == nil || *ms < stall*1000 {
					t.Errorf("metrics.latency_ms.p99 is under %v\n%s", loadStall, out)
				}
				if j.Summary.Total < seconds-0.01 || j.Summary.Total > 2*seconds || j.Summary.RequestsPerSec > rate*1.05 {
					t.Errorf("total %v s, %v requests/sec\n%s", j.Summary.Total, j.Summary.RequestsPerSec, out)
				}
			})

			t.Run("rate count", func(t *testing.T) {
				s := newLoadServer(t)
				got := summary(t, "-n", "100", "-q", "500", "-c", "2", s.URL+"/")
				if got.statuses[200] != 100 || len(got.errors) != 0 || s.requests.Load() != 100 {
					t.Errorf("statuses %v, errors %v, server %d requests; want 100\n%s", got.statuses, got.errors, s.requests.Load(), got.text)
				}
				// The last request is due 99/500 s after the first.
				if got.total < 0.198e9 || got.total > 1e9 {
					t.Errorf("Total is %.0f ns, want about 0.2 s\n%s", got.total, got.text)
				}
			})

			// Four reactors split -n and -c, and together send exactly -n
			// requests on exactly -c connections.
			t.Run("reactors count", func(t *testing.T) {
				for _, tc := range []struct{ n, c, threads int }{{1000, 8, 4}, {7, 5, 3}, {9, 2, 4}} {
					s := newLoadServer(t)
					out, stderr, code := run(t, "-n", strconv.Itoa(tc.n), "-c", strconv.Itoa(tc.c), "--worker-threads", strconv.Itoa(tc.threads), "--output-format", "json", s.URL+"/")
					if code != 0 {
						t.Fatalf("exit %d\n%s%s", code, out, stderr)
					}
					j := parseLoadJSON(t, out)
					if j.responses() != int64(tc.n) || s.requests.Load() != int64(tc.n) || s.connections.Load() != int64(tc.c) || len(j.ErrorDistribution) != 0 {
						t.Errorf("-n %d -c %d --worker-threads %d: %d responses, server %d requests on %d connections\n%s", tc.n, tc.c, tc.threads, j.responses(), s.requests.Load(), s.connections.Load(), out)
					}
					// "hello\n" is 6 bytes.
					if j.Summary.TotalData != 6*int64(tc.n) || j.Summary.SizePerRequest == nil || *j.Summary.SizePerRequest != 6 || j.Summary.SizePerSec <= 0 {
						t.Errorf("totalData %d, sizePerRequest %v, sizePerSec %v; want %d, 6 and more than 0\n%s", j.Summary.TotalData, j.Summary.SizePerRequest, j.Summary.SizePerSec, 6*tc.n, out)
					}
					if d := j.Details.DNSDialup; d.Average == nil || *d.Fastest > *d.Slowest || *d.Slowest <= 0 || j.Details.DNSLookup.Average == nil || *j.Details.DNSLookup.Slowest != 0 {
						t.Errorf("details %+v\n%s", j.Details, out)
					}
				}
			})

			// -q is the rate of the run, not of each reactor.
			t.Run("reactors rate", func(t *testing.T) {
				s := newLoadServer(t)
				const rate = 400
				out, stderr, code := run(t, "-q", strconv.Itoa(rate), "-z", "1s", "-c", "8", "--worker-threads", "4", "-w", "--output-format", "json", s.URL+"/")
				if code != 0 {
					t.Fatalf("exit %d\n%s%s", code, out, stderr)
				}
				j := parseLoadJSON(t, out)
				if n := j.responses(); n != s.requests.Load() || n > rate+1 || n < rate*9/10 {
					t.Errorf("%d responses and %d requests at the server, want about %d\n%s", n, s.requests.Load(), rate, out)
				}
				if j.Rps.Mean == nil || *j.Rps.Mean > 2*rate {
					t.Errorf("rps %+v\n%s", j.Rps, out)
				}
			})

			t.Run("burst", func(t *testing.T) {
				s := newLoadServer(t)
				got := summary(t, "--burst-delay", "200ms", "--burst-rate", "5", "-n", "15", "-c", "5", s.URL+"/")
				if got.statuses[200] != 15 || s.requests.Load() != 15 {
					t.Errorf("statuses %v, server %d requests\n%s", got.statuses, s.requests.Load(), got.text)
				}
				// The third burst is due 400 ms after the first.
				if got.total < 0.4e9 {
					t.Errorf("Total is %.0f ns, want at least 400 ms\n%s", got.total, got.text)
				}
			})

			t.Run("https", func(t *testing.T) {
				s := startLoadServer(t, true)
				out, stderr, code := run(t, "-n", "60", "-c", "3", "--insecure", "--output-format", "json", s.URL+"/")
				if code != 0 {
					t.Fatalf("exit %d\n%s%s", code, out, stderr)
				}
				j := parseLoadJSON(t, out)
				if j.responses() != 60 || s.requests.Load() != 60 || s.connections.Load() != 3 || len(j.ErrorDistribution) != 0 {
					t.Errorf("--insecure: %d responses, server %d requests on %d connections\n%s", j.responses(), s.requests.Load(), s.connections.Load(), out)
				}
				// The certificate of httptest is not signed by a system root.
				untrusted := summary(t, "-n", "4", "-c", "2", s.URL+"/")
				if len(untrusted.statuses) != 0 || len(untrusted.errors) != 1 {
					t.Errorf("a server that the system roots do not trust gave responses\n%s", untrusted.text)
				}
				for e := range untrusted.errors {
					if !strings.Contains(e, "not trusted") {
						t.Errorf("error %q, want a certificate that is not trusted", e)
					}
				}
				ca := filepath.Join(t.TempDir(), "ca.pem")
				if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: s.Certificate().Raw}), 0o644); err != nil {
					t.Fatal(err)
				}
				before := s.requests.Load()
				trusted := summary(t, "-n", "10", "-c", "2", "--cacert", ca, "-r", "3", s.URL+"/redirect")
				if trusted.statuses[200] != 10 || len(trusted.errors) != 0 || s.requests.Load()-before != 20 {
					t.Errorf("--cacert -r 3: statuses %v, errors %v, server %d requests, want 10 responses after 20 requests\n%s", trusted.statuses, trusted.errors, s.requests.Load()-before, trusted.text)
				}
			})

			t.Run("timeout", func(t *testing.T) {
				s := newLoadServer(t)
				got := summary(t, "-n", "4", "-c", "2", "-t", "100ms", s.URL+"/sleep")
				if got.errors["timeout"] != 4 || len(got.statuses) != 0 || got.total > 0.9e9 {
					t.Errorf("errors %v, statuses %v; want 4 timeouts in less than 900 ms\n%s", got.errors, got.statuses, got.text)
				}
			})

			t.Run("disable keepalive and fields", func(t *testing.T) {
				s := newLoadServer(t)
				got := summary(t, "-n", "12", "-c", "3", "--disable-keepalive", "-A", "text/x-load", "-T", "application/x-load", "-a", "user:secret", "--host", "virtual.test", s.URL+"/fields")
				if got.statuses[200] != 12 || s.requests.Load() != 12 || s.connections.Load() != 12 || s.mismatches.Load() != 0 {
					t.Errorf("statuses %v, server %d requests on %d connections, %d not as sent; want 12 on 12\n%s", got.statuses, s.requests.Load(), s.connections.Load(), s.mismatches.Load(), got.text)
				}
			})

			t.Run("redirect", func(t *testing.T) {
				s := newLoadServer(t)
				got := summary(t, "-n", "10", "-c", "2", s.URL+"/redirect")
				if got.statuses[302] != 10 || s.requests.Load() != 10 {
					t.Errorf("without -r: statuses %v, server %d requests\n%s", got.statuses, s.requests.Load(), got.text)
				}
				followed := summary(t, "-n", "10", "-c", "2", "-r", "1", s.URL+"/redirect")
				if followed.statuses[200] != 10 || s.requests.Load() != 30 || s.connections.Load() != 4 {
					t.Errorf("-r 1: statuses %v, server %d requests on %d connections\n%s", followed.statuses, s.requests.Load(), s.connections.Load(), followed.text)
				}
				looped := summary(t, "-n", "3", "-c", "1", "-r", "2", s.URL+"/loop")
				if looped.errors["Redirection limit has reached"] != 3 || len(looped.statuses) != 0 || s.requests.Load() != 39 {
					t.Errorf("a loop: statuses %v, errors %v, server %d requests\n%s", looped.statuses, looped.errors, s.requests.Load(), looped.text)
				}
			})

			t.Run("connect to", func(t *testing.T) {
				s := newLoadServer(t)
				_, port, _ := net.SplitHostPort(strings.TrimPrefix(s.URL, "http://"))
				got := summary(t, "-n", "5", "-c", "1", "--connect-to", "load.invalid:80:127.0.0.1:"+port, "http://load.invalid/")
				if got.statuses[200] != 5 || s.requests.Load() != 5 || s.host.Load() != "load.invalid" {
					t.Errorf("statuses %v, server %d requests, Host %v\n%s", got.statuses, s.requests.Load(), s.host.Load(), got.text)
				}
			})

			t.Run("body file and output", func(t *testing.T) {
				s := newLoadServer(t)
				dir := t.TempDir()
				body := filepath.Join(dir, "body")
				if err := os.WriteFile(body, []byte("payload"), 0o644); err != nil {
					t.Fatal(err)
				}
				report := filepath.Join(dir, "report.txt")
				out, stderr, code := run(t, "-n", "6", "-c", "2", "-m", "POST", "-D", body, "-H", "X-Load: yes", "-o", report, "-u", "ms", s.URL+"/echo")
				if code != 0 || out != "" {
					t.Fatalf("-o: exit %d, stdout %q\n%s", code, out, stderr)
				}
				text, err := os.ReadFile(report)
				if err != nil {
					t.Fatal(err)
				}
				got := parseLoadSummary(t, string(text))
				if got.statuses[201] != 6 || s.mismatches.Load() != 0 || !strings.Contains(got.text, " ms\n") {
					t.Errorf("-D: statuses %v, %d not as sent\n%s", got.statuses, s.mismatches.Load(), got.text)
				}
				if out, stderr, code := run(t, "-n", "3", "-c", "1", "--output-format", "quiet", s.URL+"/"); code != 0 || out != "" {
					t.Errorf("quiet: exit %d, stdout %q\n%s", code, out, stderr)
				}
				if _, stderr, code := run(t, "-n", "1", "-D", filepath.Join(dir, "missing"), s.URL+"/"); code != 1 || !strings.Contains(stderr, "load: -D ") {
					t.Errorf("-D of a missing file: exit %d, stderr %q", code, stderr)
				}
			})

			t.Run("usage", func(t *testing.T) {
				for _, tc := range []struct {
					args []string
					want string
				}{
					{[]string{"--http2", "https://127.0.0.1/"}, "load: --http2: HTTP/2 is not supported: the tool uses HTTP/1.1\n"},
					{[]string{"-w", "http://127.0.0.1/"}, "load: -w needs -z\n"},
					{[]string{"-z", "10", "http://127.0.0.1/"}, "load: -z 10 is not a duration such as 10s, 500ms or 1m\n"},
					{[]string{}, "load: give one URL\n"},
					{[]string{"-q", "0", "http://127.0.0.1/"}, "load: -q 0 must be more than 0 and not more than 1000000000\n"},
					{[]string{"--output-format", "csv", "http://127.0.0.1/"}, "load: --output-format csv is not supported: use text, json or quiet\n"},
				} {
					out, stderr, code := run(t, tc.args...)
					if code != 2 || out != "" || !strings.HasPrefix(stderr, tc.want) || !strings.Contains(stderr, "Usage: load ") {
						t.Errorf("load %v: exit %d, stdout %q, stderr %q; want exit 2 and %q", tc.args, code, out, stderr, tc.want)
					}
				}
				if out, _, code := run(t, "--help"); code != 0 || !strings.HasPrefix(out, "Usage: load ") {
					t.Errorf("load --help: exit %d, stdout %q", code, out)
				}
				if out, _, code := run(t, "-V"); code != 0 || !strings.HasPrefix(out, "fern-load ") {
					t.Errorf("load -V: exit %d, stdout %q", code, out)
				}
				if _, stderr, code := run(t, "--ipv6", "-n", "1", "http://127.0.0.1:1/"); code != 1 || !strings.Contains(stderr, "127.0.0.1 has no address") {
					t.Errorf("load --ipv6 with an IPv4 address: exit %d, stderr %q", code, stderr)
				}
			})
		})
	}
}
