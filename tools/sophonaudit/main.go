// Command sophonaudit performs a read-only audit of the live Sophon schema.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"genshintools/internal/resources"
)

func main() {
	assetsFlag := flag.String("assets", "", "comma-separated assets whose manifests should be parsed")
	timeout := flag.Duration("timeout", 90*time.Second, "overall audit timeout")
	flag.Parse()
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	provider := resources.NewSophonProvider()
	branches, err := provider.FetchBranches(ctx)
	if err != nil {
		fatal(err)
	}
	provider.BuildURL, err = branches.Current.BuildURL()
	if err != nil {
		fatal(err)
	}
	catalog, err := provider.FetchCatalog(ctx)
	if err != nil {
		fatal(err)
	}
	result := map[string]any{"version": catalog.Version, "assets": catalog.Assets(), "pre_download_available": branches.PreDownload != nil}
	if strings.TrimSpace(*assetsFlag) != "" {
		names := strings.Split(*assetsFlag, ",")
		manifest, err := provider.LoadManifest(ctx, catalog, names...)
		if err != nil {
			fatal(err)
		}
		var bytes int64
		var chunks int
		for _, file := range manifest.Files {
			bytes += file.Size
			chunks += len(file.Chunks)
		}
		result["selected"] = names
		result["files"] = len(manifest.Files)
		result["chunks"] = chunks
		result["bytes"] = bytes
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(result); err != nil {
		fatal(err)
	}
}

func fatal(err error) {
	_, _ = fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
