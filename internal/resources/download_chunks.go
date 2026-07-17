package resources

import (
	"context"
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/klauspost/compress/zstd"
)

func (d *Downloader) downloadChunkedFile(ctx context.Context, client *http.Client, item ManifestFile, destination string, attempts int, progress *atomic.Int64, report func()) error {
	part := destination + ".part"
	validBytes, firstChunk, err := verifiedChunkPrefix(ctx, part, item.Chunks)
	if err != nil {
		return err
	}
	if validBytes > 0 {
		progress.Add(validBytes)
	}
	output, err := os.OpenFile(part, os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if err := output.Truncate(validBytes); err != nil {
		output.Close()
		return err
	}
	if _, err := output.Seek(validBytes, io.SeekStart); err != nil {
		output.Close()
		return err
	}
	for index := firstChunk; index < len(item.Chunks); index++ {
		chunk := item.Chunks[index]
		if err := ctx.Err(); err != nil {
			output.Close()
			return err
		}
		compressedPath := fmt.Sprintf("%s.chunk-%d.zst.part", destination, index)
		if err := d.fetchCompressedChunk(ctx, client, chunk, compressedPath, attempts); err != nil {
			output.Close()
			return fmt.Errorf("chunk %q: %w", chunk.ID, err)
		}
		written, err := decompressChunk(compressedPath, output, chunk, func(n int64) {
			progress.Add(n)
			report()
		})
		_ = os.Remove(compressedPath)
		if err != nil {
			_ = output.Truncate(chunk.Offset)
			progress.Add(-written)
			output.Close()
			return fmt.Errorf("chunk %q: %w", chunk.ID, err)
		}
		if err := output.Sync(); err != nil {
			output.Close()
			return err
		}
	}
	if err := output.Close(); err != nil {
		return err
	}
	if err := VerifyFileContext(ctx, part, item.Size, item.Hash); err != nil {
		_ = os.Remove(part)
		progress.Add(-item.Size)
		return err
	}
	return replaceFile(part, destination)
}

func (d *Downloader) fetchCompressedChunk(ctx context.Context, client *http.Client, chunk ManifestChunk, path string, attempts int) error {
	var last error
	for attempt := 1; attempt <= attempts; attempt++ {
		offset, err := fileSize(path)
		if err != nil {
			return err
		}
		if offset > chunk.CompressedSize {
			_ = os.Remove(path)
			offset = 0
		}
		_, retry, _, err := fetchRange(ctx, client, chunk.URL, path, offset, chunk.CompressedSize, func(int64) {})
		if err == nil {
			return nil
		}
		last = err
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

func decompressChunk(path string, output io.Writer, chunk ManifestChunk, count func(int64)) (int64, error) {
	input, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer input.Close()
	decoder, err := zstd.NewReader(input, zstd.WithDecoderMaxMemory(maxSophonManifest))
	if err != nil {
		return 0, err
	}
	defer decoder.Close()
	digest, err := hashFor(chunk.Hash.Algorithm)
	if err != nil {
		return 0, err
	}
	limited := io.LimitReader(decoder, chunk.Size+1)
	written, err := io.Copy(io.MultiWriter(&countingWriter{writer: output, count: count}, digest), limited)
	if err != nil {
		return written, err
	}
	if written != chunk.Size {
		return written, fmt.Errorf("decompressed size %d, want %d", written, chunk.Size)
	}
	if !strings.EqualFold(hex.EncodeToString(digest.Sum(nil)), chunk.Hash.Digest) {
		return written, errors.New("decompressed hash mismatch")
	}
	return written, nil
}

func verifiedChunkPrefix(ctx context.Context, path string, chunks []ManifestChunk) (int64, int, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return 0, 0, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return 0, 0, err
	}
	length := info.Size()
	var valid int64
	for index, chunk := range chunks {
		if err := ctx.Err(); err != nil {
			return 0, 0, err
		}
		end := chunk.Offset + chunk.Size
		if length < end {
			if err := file.Truncate(valid); err != nil {
				return 0, 0, err
			}
			return valid, index, nil
		}
		digest, err := hashFor(chunk.Hash.Algorithm)
		if err != nil {
			return 0, 0, err
		}
		if _, err := io.Copy(digest, &contextReader{ctx: ctx, reader: io.NewSectionReader(file, chunk.Offset, chunk.Size)}); err != nil {
			return 0, 0, err
		}
		if !strings.EqualFold(hex.EncodeToString(digest.Sum(nil)), chunk.Hash.Digest) {
			if err := file.Truncate(valid); err != nil {
				return 0, 0, err
			}
			return valid, index, nil
		}
		valid = end
	}
	if length != valid {
		if err := file.Truncate(valid); err != nil {
			return 0, 0, err
		}
	}
	return valid, len(chunks), nil
}

func hashFor(algorithm string) (hash.Hash, error) {
	switch strings.ToLower(algorithm) {
	case "md5":
		return md5.New(), nil
	case "sha256":
		return sha256.New(), nil
	default:
		return nil, fmt.Errorf("unsupported hash algorithm %q", algorithm)
	}
}
