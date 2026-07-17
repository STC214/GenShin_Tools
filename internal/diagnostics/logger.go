// Package diagnostics provides the minimum always-available S02 diagnostics.
package diagnostics

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"
)

type Logger struct {
	mu   sync.Mutex
	file *os.File
}

type Entry struct {
	TimeUTC string         `json:"timeUtc"`
	Level   string         `json:"level"`
	Message string         `json:"message"`
	Fields  map[string]any `json:"fields,omitempty"`
}

func Open(directory string) (*Logger, error) {
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return nil, fmt.Errorf("create log directory: %w", err)
	}
	path := filepath.Join(directory, "genshin-tools.log")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open log: %w", err)
	}
	return &Logger{file: file}, nil
}

func (l *Logger) Write(level, message string, fields map[string]any) {
	if l == nil {
		return
	}
	entry := Entry{TimeUTC: time.Now().UTC().Format(time.RFC3339Nano), Level: level, Message: message, Fields: fields}
	data, err := json.Marshal(entry)
	if err != nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file != nil {
		_, _ = l.file.Write(append(data, '\n'))
	}
}

func (l *Logger) Info(message string, fields map[string]any)  { l.Write("info", message, fields) }
func (l *Logger) Error(message string, fields map[string]any) { l.Write("error", message, fields) }

func (l *Logger) Panic(message string, value any) {
	buffer := make([]byte, 64<<10)
	length := runtime.Stack(buffer, true)
	l.Error(message, map[string]any{"panic": fmt.Sprint(value), "stack": string(buffer[:length])})
}

func (l *Logger) Close() error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file == nil {
		return nil
	}
	if err := l.file.Sync(); err != nil {
		return err
	}
	err := l.file.Close()
	l.file = nil
	return err
}
