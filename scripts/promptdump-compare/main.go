package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/konglong87/super-niuma/internal/promptdump"
)

func main() {
	goPath := flag.String("go", "", "niuma prompt dump JSONL path")
	upstreamPath := flag.String("upstream", "", "upstream Claude Code dumpPrompts JSONL path")
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "Usage: go run ./scripts/promptdump-compare --go <go.jsonl> --upstream <claude-code.jsonl>\n\n")
		flag.PrintDefaults()
	}
	flag.Parse()
	if *goPath == "" || *upstreamPath == "" || flag.NArg() != 0 {
		flag.Usage()
		os.Exit(2)
	}

	report, err := promptdump.ComparePromptDumps(*goPath, *upstreamPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "compare prompt dumps: %v\n", err)
		os.Exit(2)
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(report); err != nil {
		fmt.Fprintf(os.Stderr, "write report: %v\n", err)
		os.Exit(2)
	}
	if !report.OK {
		os.Exit(1)
	}
}
