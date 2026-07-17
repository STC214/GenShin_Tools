package resources

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type Progress struct {
	FilesDone  int
	FilesTotal int
	BytesDone  int64
	BytesTotal int64
	Speed      float64
	ETA        time.Duration
}

type Downloader struct {
	Client      *http.Client
	Concurrency int
	MaxAttempts int
	RetryDelay  time.Duration
	OnProgress  func(Progress)
}

func NewDownloader() *Downloader {
	return &Downloader{
		Client:      &http.Client{Timeout: 2 * time.Minute},
		Concurrency: 4,
		MaxAttempts: 4,
		RetryDelay:  500 * time.Millisecond,
	}
}

func (d *Downloader) Download(ctx context.Context, manifest Manifest, stagingRoot string) error {
	if err := manifest.Validate(); err != nil {
		return err
	}
	if stagingRoot == "" {
		return errors.New("staging root is empty")
	}
	concurrency := d.Concurrency
	if concurrency < 1 || concurrency > 16 {
		return fmt.Errorf("download concurrency %d is outside 1..16", concurrency)
	}
	attempts := d.MaxAttempts
	if attempts < 1 || attempts > 10 {
		return fmt.Errorf("max attempts %d is outside 1..10", attempts)
	}
	client := d.Client
	if client == nil {
		client = NewDownloader().Client
	}
	var total int64
	for _, file := range manifest.Files {
		if file.Size > 0 && total > (1<<63-1)-file.Size {
			return errors.New("manifest total size overflows int64")
		}
		total += file.Size
	}
	if err := RequireDiskSpace(stagingRoot, uint64(total)); err != nil {
		return err
	}
	if err := os.MkdirAll(stagingRoot, 0o755); err != nil {
		return fmt.Errorf("create staging root: %w", err)
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	jobs := make(chan ManifestFile)
	errCh := make(chan error, 1)
	var downloaded atomic.Int64
	var filesDone atomic.Int64
	started := time.Now()
	var callbackMu sync.Mutex
	report := func() {
		if d.OnProgress == nil {
			return
		}
		bytesDone := downloaded.Load()
		elapsed := time.Since(started).Seconds()
		var speed float64
		if elapsed > 0 {
			speed = float64(bytesDone) / elapsed
		}
		var eta time.Duration
		if speed > 0 && bytesDone < total {
			eta = time.Duration(float64(total-bytesDone)/speed) * time.Second
		}
		callbackMu.Lock()
		d.OnProgress(Progress{FilesDone: int(filesDone.Load()), FilesTotal: len(manifest.Files), BytesDone: bytesDone, BytesTotal: total, Speed: speed, ETA: eta})
		callbackMu.Unlock()
	}

	var workers sync.WaitGroup
	for range concurrency {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for file := range jobs {
				if err := d.downloadFile(ctx, client, file, stagingRoot, attempts, &downloaded, report); err != nil {
					select {
					case errCh <- fmt.Errorf("download %q: %w", file.Path, err):
						cancel()
					default:
					}
					return
				}
				filesDone.Add(1)
				report()
			}
		}()
	}
	feedDone := make(chan struct{})
	go func() {
		defer close(feedDone)
		defer close(jobs)
		for _, file := range manifest.Files {
			select {
			case jobs <- file:
			case <-ctx.Done():
				return
			}
		}
	}()
	workers.Wait()
	<-feedDone
	select {
	case err := <-errCh:
		return err
	default:
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return nil
}

func (d *Downloader) downloadFile(ctx context.Context, client *http.Client, item ManifestFile, root string, attempts int, progress *atomic.Int64, report func()) error {
	destination := filepath.Join(root, item.Path)
	if err := ensureContained(root, destination); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return err
	}
	if err := VerifyFile(destination, item.Size, item.Hash); err == nil {
		progress.Add(item.Size)
		return nil
	}
	part := destination + ".part"
	var last error
	countedExisting := false
	for attempt := 1; attempt <= attempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		before, err := fileSize(part)
		if err != nil {
			return err
		}
		if before > item.Size {
			if err := os.Remove(part); err != nil {
				return fmt.Errorf("discard oversized partial file: %w", err)
			}
			before = 0
		}
		if !countedExisting && before > 0 {
			progress.Add(before)
			countedExisting = true
		}
		wrote, retry, reset, err := fetchRange(ctx, client, item.URL, part, before, item.Size, func(n int64) {
			progress.Add(n)
			report()
		})
		if reset && before > 0 {
			progress.Add(-before)
		}
		if err == nil {
			if err = VerifyFile(part, item.Size, item.Hash); err == nil {
				if err = replaceFile(part, destination); err == nil {
					return nil
				}
			}
		}
		last = err
		if err != nil && before+wrote > 0 && !retry {
			progress.Add(-(before + wrote))
			_ = os.Remove(part)
		}
		if !retry || attempt == attempts {
			break
		}
		delay := d.RetryDelay * time.Duration(1<<(attempt-1))
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return last
}

func fetchRange(ctx context.Context, client *http.Client, rawURL, path string, offset, expectedSize int64, count func(int64)) (int64, bool, bool, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return 0, false, false, err
	}
	if offset > 0 {
		request.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
	}
	response, err := client.Do(request)
	if err != nil {
		return 0, isTransientNetworkError(err), false, err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusRequestedRangeNotSatisfiable {
		return 0, false, false, fmt.Errorf("server rejected resume offset %d", offset)
	}
	if response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= 500 {
		return 0, true, false, fmt.Errorf("server returned %s", response.Status)
	}
	if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusPartialContent {
		return 0, false, false, fmt.Errorf("server returned %s", response.Status)
	}
	reset := offset > 0 && response.StatusCode == http.StatusOK
	flags := os.O_CREATE | os.O_WRONLY
	if offset > 0 && response.StatusCode == http.StatusPartialContent {
		if err := validateContentRange(response.Header.Get("Content-Range"), offset, expectedSize); err != nil {
			return 0, false, false, err
		}
		flags |= os.O_APPEND
	} else {
		offset = 0
		flags |= os.O_TRUNC
	}
	file, err := os.OpenFile(path, flags, 0o644)
	if err != nil {
		return 0, false, reset, err
	}
	written, copyErr := io.Copy(&countingWriter{writer: file, count: count}, response.Body)
	syncErr := file.Sync()
	closeErr := file.Close()
	if copyErr != nil {
		return written, isTransientNetworkError(copyErr), reset, copyErr
	}
	if syncErr != nil {
		return written, false, reset, syncErr
	}
	if closeErr != nil {
		return written, false, reset, closeErr
	}
	if offset+written != expectedSize {
		return written, true, reset, fmt.Errorf("downloaded size %d, want %d", offset+written, expectedSize)
	}
	return written, false, reset, nil
}

type countingWriter struct {
	writer io.Writer
	count  func(int64)
}

func (w *countingWriter) Write(data []byte) (int, error) {
	n, err := w.writer.Write(data)
	if n > 0 {
		w.count(int64(n))
	}
	return n, err
}

func validateContentRange(value string, offset, total int64) error {
	prefix := "bytes " + strconv.FormatInt(offset, 10) + "-"
	if !strings.HasPrefix(value, prefix) || !strings.HasSuffix(value, "/"+strconv.FormatInt(total, 10)) {
		return fmt.Errorf("invalid Content-Range %q", value)
	}
	return nil
}

func fileSize(path string) (int64, error) {
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	if !info.Mode().IsRegular() {
		return 0, fmt.Errorf("partial path is not a regular file")
	}
	return info.Size(), nil
}

func ensureContained(root, candidate string) error {
	relative, err := filepath.Rel(root, candidate)
	if err != nil || relative == ".." || strings.HasPrefix(relative, `..\`) || filepath.IsAbs(relative) {
		return fmt.Errorf("path %q escapes root %q", candidate, root)
	}
	return nil
}

func isTransientNetworkError(err error) bool {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var netErr net.Error
	return errors.As(err, &netErr)
}
