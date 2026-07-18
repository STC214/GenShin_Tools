package diagnostics

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"time"

	"genshintools/internal/buildinfo"
)

const maxDiagnosticLogBytes = 256 << 10

type ResourceReport struct {
	CPUPercent float64 `json:"cpuPercent"`
	Goroutines int     `json:"goroutines"`
	Threads    uint32  `json:"threads"`
	Handles    uint32  `json:"handles"`
	USER       uint32  `json:"userObjects"`
	GDI        uint32  `json:"gdiObjects"`
	HeapBytes  uint64  `json:"heapBytes"`
}

type ConfigSummary struct {
	SchemaVersion int             `json:"schemaVersion"`
	Language      string          `json:"language"`
	Theme         string          `json:"theme"`
	Tray          bool            `json:"minimizeToTray"`
	RememberSize  bool            `json:"rememberWindowSize"`
	MinimumSize   bool            `json:"enforceMinimumSize"`
	Priority      string          `json:"processPriority"`
	CPUWarning    bool            `json:"cpuWarningEnabled"`
	InputMode     string          `json:"inputMode"`
	InputInterval int             `json:"inputIntervalMs"`
	PluginCount   int             `json:"pluginCount"`
	PluginSafe    bool            `json:"pluginSafeMode"`
	Features      map[string]bool `json:"features"`
}

type ExportInput struct {
	Build          buildinfo.Info
	UpstreamCommit string
	MonitorCount   int
	DPI            uint32
	Resources      ResourceReport
	Config         ConfigSummary
	LogPath        string
	Now            time.Time
}

type recentLog struct {
	TimeUTC string `json:"timeUtc"`
	Level   string `json:"level"`
	Message string `json:"message"`
}

type diagnosticReport struct {
	GeneratedUTC   string         `json:"generatedUtc"`
	Build          buildinfo.Info `json:"build"`
	UpstreamCommit string         `json:"upstreamCommit"`
	System         map[string]any `json:"system"`
	Resources      ResourceReport `json:"resources"`
	Config         ConfigSummary  `json:"configSummary"`
	RecentLogs     []recentLog    `json:"recentLogs"`
}

// Export writes a privacy-filtered report through a flushed temporary file
// and atomically replaces the selected destination.
func Export(destination string, input ExportInput) error {
	if destination == "" || filepath.Base(destination) == "." {
		return fmt.Errorf("diagnostic destination is invalid")
	}
	logs, err := readRecentLogs(input.LogPath, 100)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("read recent logs: %w", err)
	}
	now := input.Now
	if now.IsZero() {
		now = time.Now()
	}
	report := diagnosticReport{
		GeneratedUTC:   now.UTC().Format(time.RFC3339Nano),
		Build:          input.Build,
		UpstreamCommit: input.UpstreamCommit,
		System: map[string]any{
			"os": runtime.GOOS, "architecture": runtime.GOARCH, "logicalProcessors": runtime.NumCPU(),
			"monitorCount": input.MonitorCount, "windowDpi": input.DPI,
		},
		Resources:  input.Resources,
		Config:     input.Config,
		RecentLogs: logs,
	}
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("encode diagnostics: %w", err)
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return fmt.Errorf("create diagnostic directory: %w", err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(destination), ".genshin-tools-diagnostics-*.tmp")
	if err != nil {
		return fmt.Errorf("create diagnostic temporary file: %w", err)
	}
	temporaryPath := temporary.Name()
	committed := false
	defer func() {
		_ = temporary.Close()
		if !committed {
			_ = os.Remove(temporaryPath)
		}
	}()
	if _, err := temporary.Write(data); err != nil {
		return fmt.Errorf("write diagnostics: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("flush diagnostics: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close diagnostics: %w", err)
	}
	if err := replaceMarker(temporaryPath, destination); err != nil {
		return fmt.Errorf("commit diagnostics: %w", err)
	}
	committed = true
	return nil
}

var privateLogText = regexp.MustCompile(`(?i)(https?://\S+|[a-z]:\\[^\r\n\t"]+)`)

func readRecentLogs(path string, limit int) ([]recentLog, error) {
	if path == "" || limit < 1 {
		return []recentLog{}, nil
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	if info, statErr := file.Stat(); statErr == nil && info.Size() > maxDiagnosticLogBytes {
		if _, err := file.Seek(-maxDiagnosticLogBytes, io.SeekEnd); err != nil {
			return nil, err
		}
		reader := bufio.NewReader(file)
		_, _ = reader.ReadString('\n')
		return scanRecentLogs(reader, limit), nil
	}
	return scanRecentLogs(file, limit), nil
}

func scanRecentLogs(reader io.Reader, limit int) []recentLog {
	items := make([]recentLog, 0, limit)
	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		var entry Entry
		if json.Unmarshal(scanner.Bytes(), &entry) != nil {
			continue
		}
		items = append(items, recentLog{TimeUTC: entry.TimeUTC, Level: entry.Level, Message: privateLogText.ReplaceAllString(entry.Message, "[redacted]")})
		if len(items) > limit {
			copy(items, items[len(items)-limit:])
			items = items[:limit]
		}
	}
	return items
}
