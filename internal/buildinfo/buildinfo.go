package buildinfo

import (
	"fmt"
	"runtime"
)

// These values are replaced by scripts/build.ps1 through -ldflags -X.
var (
	Version       = "0.0.0-dev"
	Commit        = "unknown"
	BuildTimeUTC  = "unknown"
	Configuration = "development"
)

// Info is the machine-readable build identity shown by diagnostics and --version-json.
type Info struct {
	Product       string `json:"product"`
	Version       string `json:"version"`
	Commit        string `json:"commit"`
	BuildTimeUTC  string `json:"buildTimeUtc"`
	Configuration string `json:"configuration"`
	GoVersion     string `json:"goVersion"`
	Platform      string `json:"platform"`
}

// Current returns the complete build identity.
func Current() Info {
	return Info{
		Product:       "Genshin Tools",
		Version:       Version,
		Commit:        Commit,
		BuildTimeUTC:  BuildTimeUTC,
		Configuration: Configuration,
		GoVersion:     runtime.Version(),
		Platform:      runtime.GOOS + "/" + runtime.GOARCH,
	}
}

// String returns a concise human-readable build identity.
func (i Info) String() string {
	return fmt.Sprintf("%s %s (%s, %s, %s)", i.Product, i.Version, i.Commit, i.Configuration, i.Platform)
}
