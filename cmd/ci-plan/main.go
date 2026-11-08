package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/jakechampion/lang/internal/ciplan"
)

func main() {
	root := flag.String("root", ".", "repository root")
	full := flag.Bool("full", false, "select broad validation")
	table := flag.Bool("table", false, "print the dependency filters")
	serve := flag.Bool("serve", false, "read multiple JSON requests")
	flag.Parse()
	encoder := json.NewEncoder(os.Stdout)
	if *table {
		config, err := ciplan.ReadConfig(*root)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		if err := encoder.Encode(config.Targets); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	graph, err := ciplan.Load(*root)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if *serve {
		decoder := json.NewDecoder(os.Stdin)
		for {
			var paths []string
			if err := decoder.Decode(&paths); err != nil {
				if err == io.EOF {
					return
				}
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
			plan := graph.Full("full validation requested")
			if paths != nil {
				plan = graph.Select(paths)
			}
			if err := encoder.Encode(plan); err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
		}
	}
	var plan ciplan.Plan
	if *full {
		plan = graph.Full("full validation requested")
	} else {
		var paths []string
		if err := json.NewDecoder(os.Stdin).Decode(&paths); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		if paths == nil {
			fmt.Fprintln(os.Stderr, "changed paths must be a JSON array")
			os.Exit(1)
		}
		plan = graph.Select(paths)
	}
	if err := json.NewEncoder(os.Stdout).Encode(plan); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
