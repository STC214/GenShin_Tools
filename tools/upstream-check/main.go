package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"genshintools/internal/upstreamaudit"
)

func main() {
	root := flag.String("root", ".", "project root")
	apiBase := flag.String("api-base", "https://api.github.com", "GitHub API base URL")
	updateBaseline := flag.Bool("update-baseline", false, "update upstream.lock.json after complete human disposition")
	dispositionPath := flag.String("disposition", "", "review disposition JSON required with --update-baseline")
	flag.Parse()
	if err := run(*root, *apiBase, *updateBaseline, *dispositionPath); err != nil {
		fmt.Fprintln(os.Stderr, "upstream check failed:", err)
		os.Exit(1)
	}
}

func run(root, apiBase string, updateBaseline bool, dispositionPath string) error {
	lockPath := filepath.Join(root, "upstream.lock.json")
	lockData, err := os.ReadFile(lockPath)
	if err != nil {
		return err
	}
	lock, err := upstreamaudit.LoadLock(bytes.NewReader(lockData))
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	comparison, err := (upstreamaudit.Client{BaseURL: apiBase, Token: os.Getenv("GITHUB_TOKEN")}).Compare(ctx, lock)
	if err != nil {
		return err
	}
	report, err := upstreamaudit.BuildReport(lock, comparison)
	if err != nil {
		return err
	}
	destination, err := upstreamaudit.WriteReport(filepath.Join(root, "artifacts", "upstream-check"), report)
	if err != nil {
		return err
	}
	fmt.Println(destination)
	if updateBaseline {
		if dispositionPath == "" {
			return fmt.Errorf("--disposition is required with --update-baseline")
		}
		dispositionFile, err := os.Open(dispositionPath)
		if err != nil {
			return err
		}
		disposition, loadErr := upstreamaudit.LoadDisposition(dispositionFile)
		closeErr := dispositionFile.Close()
		if loadErr != nil {
			return loadErr
		}
		if closeErr != nil {
			return closeErr
		}
		if err := upstreamaudit.UpdateBaseline(lockPath, lockData, lock, report, disposition); err != nil {
			return err
		}
		fmt.Println("updated", lockPath)
		return nil
	}
	if report.Counts[string(upstreamaudit.DependencyRisk)] > 0 || report.Counts[string(upstreamaudit.ReviewRequired)] > 0 {
		return fmt.Errorf("manual review required: dependency_risk=%d review_required=%d", report.Counts[string(upstreamaudit.DependencyRisk)], report.Counts[string(upstreamaudit.ReviewRequired)])
	}
	return nil
}
