package plugins

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

var errFileTooLarge = errors.New("file exceeds safety limit")

func readFileBounded(path string, maximum int64) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maximum+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maximum {
		return nil, fmt.Errorf("%w: %s exceeds %d bytes", errFileTooLarge, filepath.Base(path), maximum)
	}
	return data, nil
}
