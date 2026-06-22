package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
)

func main() {
	sourceRoot := flag.String("source-root", "", "provider source root")
	outPath := flag.String("out", "", "write JSON facts to this file instead of stdout")
	flag.Parse()

	if *sourceRoot == "" {
		fmt.Fprintln(os.Stderr, "error: --source-root is required")
		os.Exit(2)
	}

	report, err := Collect(*sourceRoot)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	var out *os.File
	if *outPath == "" {
		out = os.Stdout
	} else {
		f, err := os.Create(*outPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: create output: %v\n", err)
			os.Exit(1)
		}
		defer func() {
			if err := f.Close(); err != nil {
				fmt.Fprintf(os.Stderr, "error: close output: %v\n", err)
				os.Exit(1)
			}
		}()
		out = f
	}

	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	if err := enc.Encode(report); err != nil {
		fmt.Fprintf(os.Stderr, "error: encode JSON: %v\n", err)
		os.Exit(1)
	}
}
