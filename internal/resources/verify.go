package resources

import (
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash"
	"io"
	"os"
	"strings"
)

func VerifyFile(path string, expectedSize int64, expected Hash) error {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return fmt.Errorf("stat %s: %w", path, err)
	}
	if info.Size() != expectedSize {
		return fmt.Errorf("size mismatch: got %d, want %d", info.Size(), expectedSize)
	}
	var digest hash.Hash
	switch strings.ToLower(expected.Algorithm) {
	case "md5":
		digest = md5.New()
	case "sha256":
		digest = sha256.New()
	default:
		return fmt.Errorf("unsupported hash algorithm %q", expected.Algorithm)
	}
	if _, err := io.Copy(digest, file); err != nil {
		return fmt.Errorf("hash %s: %w", path, err)
	}
	actual := hex.EncodeToString(digest.Sum(nil))
	if !strings.EqualFold(actual, expected.Digest) {
		return fmt.Errorf("hash mismatch: got %s, want %s", actual, expected.Digest)
	}
	return nil
}
