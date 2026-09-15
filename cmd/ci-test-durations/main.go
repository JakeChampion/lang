// ci-test-durations measures complete top-level Go test lifetimes, including
// their parallel children, from fresh full test2json event streams.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"
)

type testKey struct{ pkg, name string }

type runningTest struct {
	start, last, pauseStart time.Time
	paused                  time.Duration
}

var cachedOutput = regexp.MustCompile(`^ok\s+(\S+)\s+\(cached\)\s*$`)

func durations(r io.Reader) (map[string]float64, error) {
	active := map[testKey]runningTest{}
	records := map[testKey]float64{}
	cached := map[string]bool{}
	decoder := json.NewDecoder(r)
	for {
		var event struct {
			Time                          time.Time
			Action, Package, Test, Output string
		}
		if err := decoder.Decode(&event); err != nil {
			if err == io.EOF {
				break
			}
			return nil, fmt.Errorf("invalid test JSON: %w", err)
		}
		if event.Test == "" {
			if event.Action == "output" {
				match := cachedOutput.FindStringSubmatch(event.Output)
				if len(match) > 0 && match[1] == event.Package {
					cached[event.Package] = true
				}
			}
			continue
		}
		if strings.Contains(event.Test, "/") {
			continue
		}
		switch event.Action {
		case "run", "pause", "cont", "pass", "fail", "skip":
		default:
			continue
		}
		if event.Time.IsZero() {
			return nil, fmt.Errorf("missing timestamp: %s", event.Test)
		}
		key := testKey{event.Package, event.Test}
		state, exists := active[key]
		if event.Action == "run" {
			if exists {
				return nil, fmt.Errorf("duplicate start: %s", event.Test)
			}
			active[key] = runningTest{start: event.Time, last: event.Time}
			continue
		}
		if !exists {
			return nil, fmt.Errorf("missing run event for %s; supply full --jsonfile output, not terminal-only timing events", event.Test)
		}
		if event.Time.Before(state.last) {
			return nil, fmt.Errorf("time went backwards: %s", event.Test)
		}
		state.last = event.Time
		switch event.Action {
		case "pause":
			if !state.pauseStart.IsZero() {
				return nil, fmt.Errorf("duplicate pause: %s", event.Test)
			}
			state.pauseStart = event.Time
			active[key] = state
		case "cont":
			if state.pauseStart.IsZero() {
				return nil, fmt.Errorf("invalid resume: %s", event.Test)
			}
			state.paused += event.Time.Sub(state.pauseStart)
			state.pauseStart = time.Time{}
			active[key] = state
		default:
			if !state.pauseStart.IsZero() {
				return nil, fmt.Errorf("test finished while paused: %s", event.Test)
			}
			seconds := (event.Time.Sub(state.start) - state.paused).Seconds()
			if seconds < 0 {
				return nil, fmt.Errorf("negative duration: %s", event.Test)
			}
			if event.Action != "skip" {
				records[key] = max(records[key], seconds)
			}
			delete(active, key)
		}
	}
	// Timeouts can leave unfinished tests. Keep only completed observations.
	// Cached Go output replays old events with fresh timestamps: exclude it.
	result := map[string]float64{}
	for key, seconds := range records {
		if !cached[key.pkg] {
			result[key.name] = max(result[key.name], seconds)
		}
	}
	return result, nil
}

func extract(files []string) (map[string]float64, error) {
	if len(files) == 0 {
		return nil, errors.New("no test JSON files supplied")
	}
	maximum := map[string]float64{}
	for _, path := range files {
		f, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		rows, readErr := durations(f)
		if err := errors.Join(readErr, f.Close()); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		for name, seconds := range rows {
			maximum[name] = max(maximum[name], seconds)
		}
	}
	return maximum, nil
}

func main() {
	rows, err := extract(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, "ci-test-durations:", err)
		os.Exit(1)
	}
	names := make([]string, 0, len(rows))
	for name := range rows {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		fmt.Printf("%s\t%.2f\n", name, rows[name])
	}
}
