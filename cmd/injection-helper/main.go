package main

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"genshintools/internal/injection"
)

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(arguments []string) (exitCode int) {
	if len(arguments) != 2 || arguments[0] != "--request" {
		fmt.Fprintln(os.Stderr, "usage: GenshinTools-injector.exe --request <request.json>")
		return 2
	}
	requestPath, err := filepath.Abs(arguments[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	helperPath, err := os.Executable()
	if err != nil || injection.ValidateHelperRequestScope(requestPath, helperPath, nil) != nil {
		fmt.Fprintln(os.Stderr, "request is outside the helper portable staging layout")
		return 2
	}
	request, err := injection.LoadHelperRequest(requestPath)
	if err != nil {
		result := injection.HelperResult{ProtocolVersion: injection.ProtocolVersion, RequestID: "invalid", Code: "invalid_request", Error: err.Error(), CompletedUTC: time.Now().UTC().Format(time.RFC3339Nano)}
		_ = injection.WriteHelperResult(requestPath, result)
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	if err := injection.ValidateHelperRequestScope(requestPath, helperPath, &request); err != nil {
		result := injection.HelperResult{ProtocolVersion: injection.ProtocolVersion, RequestID: request.RequestID, Code: "invalid_scope", Error: err.Error(), CompletedUTC: time.Now().UTC().Format(time.RFC3339Nano)}
		_ = injection.WriteHelperResult(requestPath, result)
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	result := injection.ExecuteHelper(request)
	if err := injection.WriteHelperResult(requestPath, result); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if !result.Success {
		fmt.Fprintln(os.Stderr, result.Error)
		return 1
	}
	return 0
}
