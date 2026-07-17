package main

import (
	"bytes"
	"encoding/binary"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

func TestGenerateIsDeterministicAndDecodable(t *testing.T) {
	first := filepath.Join(t.TempDir(), "first.ico")
	second := filepath.Join(t.TempDir(), "second.ico")
	if err := generate(first); err != nil {
		t.Fatalf("generate first icon: %v", err)
	}
	if err := generate(second); err != nil {
		t.Fatalf("generate second icon: %v", err)
	}

	firstBytes, err := os.ReadFile(first)
	if err != nil {
		t.Fatalf("read first icon: %v", err)
	}
	secondBytes, err := os.ReadFile(second)
	if err != nil {
		t.Fatalf("read second icon: %v", err)
	}
	if !bytes.Equal(firstBytes, secondBytes) {
		t.Fatal("identical inputs produced different ICO bytes")
	}
	if len(firstBytes) < 6 || binary.LittleEndian.Uint16(firstBytes[0:2]) != 0 || binary.LittleEndian.Uint16(firstBytes[2:4]) != 1 {
		t.Fatal("invalid ICO header")
	}
	count := int(binary.LittleEndian.Uint16(firstBytes[4:6]))
	if count != len(sizes) {
		t.Fatalf("ICO image count = %d, want %d", count, len(sizes))
	}

	for index, wantSize := range sizes {
		entry := 6 + index*16
		length := int(binary.LittleEndian.Uint32(firstBytes[entry+8 : entry+12]))
		offset := int(binary.LittleEndian.Uint32(firstBytes[entry+12 : entry+16]))
		if length <= 0 || offset < 0 || offset+length > len(firstBytes) {
			t.Fatalf("entry %d has invalid range offset=%d length=%d", index, offset, length)
		}
		decoded, err := png.Decode(bytes.NewReader(firstBytes[offset : offset+length]))
		if err != nil {
			t.Fatalf("decode entry %d PNG: %v", index, err)
		}
		if decoded.Bounds().Dx() != wantSize || decoded.Bounds().Dy() != wantSize {
			t.Fatalf("entry %d size = %dx%d, want %dx%d", index, decoded.Bounds().Dx(), decoded.Bounds().Dy(), wantSize, wantSize)
		}
	}
}
