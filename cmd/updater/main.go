package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"genshintools/internal/selfupdate"
)

type executeFunc func(context.Context, selfupdate.UpdateLayout, selfupdate.UpdaterRequest, *selfupdate.UpdaterHooks) error

func main() {
	helperPath, err := os.Executable()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(run(os.Args[1:], os.Stderr, helperPath, selfupdate.ExecuteUpdate))
}

func run(arguments []string, stderr io.Writer, helperPath string, execute executeFunc) int {
	if len(arguments) != 2 || arguments[0] != "--request" {
		fmt.Fprintln(stderr, "usage: GenshinTools-updater.exe --request <update-request.json>")
		return 2
	}
	requestPath, err := filepath.Abs(arguments[1])
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	layout, err := selfupdate.ResolveUpdaterScope(helperPath, requestPath)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	request, err := selfupdate.LoadUpdaterRequest(requestPath)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	if err := execute(context.Background(), layout, request, nil); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	_ = os.Remove(requestPath)
	return 0
}
