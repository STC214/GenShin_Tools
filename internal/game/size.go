package game

import (
	"context"
	"io/fs"
	"path/filepath"
)

type SizeProgress struct {
	Bytes uint64
	Files uint64
}

// DirectorySize is read-only, cancellation-aware, and tolerates individual
// unreadable entries while returning their count to the caller.
func DirectorySize(ctx context.Context, root string, progress func(SizeProgress)) (SizeProgress, uint64, error) {
	var total SizeProgress
	var skipped uint64
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if walkErr != nil {
			skipped++
			if entry != nil && entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			skipped++
			return nil
		}
		total.Files++
		if info.Size() > 0 {
			total.Bytes += uint64(info.Size())
		}
		if progress != nil && total.Files%256 == 0 {
			progress(total)
		}
		return nil
	})
	if progress != nil {
		progress(total)
	}
	return total, skipped, err
}
