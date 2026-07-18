package main

import (
	"fmt"
	"io"
	"os"

	"genshintools/internal/buildinfo"
	"genshintools/internal/paths"
	"genshintools/internal/selfupdate"
	"genshintools/internal/shell"
)

func launchApplication(stderr io.Writer) int {
	executable, err := os.Executable()
	if err != nil {
		fmt.Fprintf(stderr, "locate executable: %v\n", err)
		return 1
	}
	layout, err := paths.ForExecutable(executable)
	if err != nil {
		fmt.Fprintf(stderr, "resolve portable layout: %v\n", err)
		return 1
	}
	if err := layout.Ensure(); err != nil {
		fmt.Fprintf(stderr, "create portable data directories: %v\n", err)
		return 1
	}
	if status, err := selfupdate.RecoverAtStartup(layout.Root, buildinfo.Version); err != nil {
		fmt.Fprintf(stderr, "update recovery warning (%s): %v\n", status, err)
	}
	if err := shell.Run(layout, buildinfo.Current()); err != nil {
		fmt.Fprintf(stderr, "run application: %v\n", err)
		return 1
	}
	return 0
}
