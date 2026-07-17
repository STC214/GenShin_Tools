package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"genshintools/internal/buildinfo"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) > 1 {
		fmt.Fprintln(stderr, "usage: GenshinTools.exe [--version|--version-json]")
		return 2
	}

	if len(args) == 1 {
		switch args[0] {
		case "--version":
			fmt.Fprintln(stdout, buildinfo.Current().String())
			return 0
		case "--version-json":
			if err := json.NewEncoder(stdout).Encode(buildinfo.Current()); err != nil {
				fmt.Fprintf(stderr, "encode version information: %v\n", err)
				return 1
			}
			return 0
		default:
			fmt.Fprintf(stderr, "unknown argument: %s\n", args[0])
			return 2
		}
	}

	return launchApplication(stderr)
}
