// launchfixture is built only by S05 integration tests. It records the exact
// argv and working directory received from the launcher, then exits quickly.
package main

import (
	"encoding/json"
	"os"
	"strconv"
	"time"
)

func main() {
	workingDirectory, _ := os.Getwd()
	result := struct {
		PID              int      `json:"pid"`
		WorkingDirectory string   `json:"workingDirectory"`
		Arguments        []string `json:"arguments"`
	}{os.Getpid(), workingDirectory, os.Args[1:]}
	data, _ := json.Marshal(result)
	if path := os.Getenv("GENSHINTOOLS_S05_RESULT"); path != "" {
		_ = os.WriteFile(path, data, 0o644)
	}
	if value, _ := strconv.Atoi(os.Getenv("GENSHINTOOLS_S05_SLEEP_MS")); value > 0 {
		time.Sleep(time.Duration(value) * time.Millisecond)
	}
	code, _ := strconv.Atoi(os.Getenv("GENSHINTOOLS_S05_EXIT_CODE"))
	os.Exit(code)
}
