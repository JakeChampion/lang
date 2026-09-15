package main

import (
	"bufio"
	"fmt"
	"go/token"
	"io"
	"math"
	"slices"
	"strconv"
	"strings"
)

// Unselected names may belong to another target or an older inventory. They
// cannot add a test: assignment always starts with the actual binary's list.
func readWeights(r io.Reader) (map[string]float64, error) {
	weights := make(map[string]float64)
	scanner := bufio.NewScanner(r)
	for line := 1; scanner.Scan(); line++ {
		row := strings.TrimSpace(scanner.Text())
		if row == "" || strings.HasPrefix(row, "#") {
			continue
		}
		fields := strings.Fields(row)
		if len(fields) != 2 || !token.IsIdentifier(fields[0]) || !strings.HasPrefix(fields[0], "Test") {
			return nil, fmt.Errorf("line %d: require a test name and positive duration", line)
		}
		weight, err := strconv.ParseFloat(fields[1], 64)
		if err != nil || weight <= 0 || math.IsNaN(weight) || math.IsInf(weight, 0) {
			return nil, fmt.Errorf("line %d: invalid duration %q", line, fields[1])
		}
		if _, exists := weights[fields[0]]; exists {
			return nil, fmt.Errorf("line %d: duplicate test %s", line, fields[0])
		}
		weights[fields[0]] = weight
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return weights, nil
}

func weightedInventory(output []byte, workers int, weights map[string]float64) ([][]string, error) {
	groups, err := inventory(output, workers)
	if err != nil || weights == nil {
		return groups, err
	}
	names := strings.Fields(string(output))
	weight := func(name string) float64 {
		if value, ok := weights[name]; ok {
			return value
		}
		return 1
	}
	ordered := slices.Clone(names)
	slices.SortFunc(ordered, func(a, b string) int {
		if weight(a) > weight(b) {
			return -1
		}
		if weight(a) < weight(b) {
			return 1
		}
		return strings.Compare(a, b)
	})
	loads := make([]float64, workers)
	assigned := make(map[string]int, len(names))
	for _, name := range ordered {
		worker := 0
		for i := 1; i < workers; i++ {
			if loads[i] < loads[worker] {
				worker = i
			}
		}
		loads[worker] += weight(name)
		if math.IsInf(loads[worker], 0) {
			return nil, fmt.Errorf("test weights overflow worker %d load", worker)
		}
		assigned[name] = worker
	}
	groups = make([][]string, workers)
	// Keep registration order within each group. The test binary executes in
	// that order regardless of the ordering of alternatives in -test.run.
	for _, name := range names {
		worker := assigned[name]
		groups[worker] = append(groups[worker], name)
	}
	return groups, nil
}
