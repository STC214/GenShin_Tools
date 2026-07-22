package main

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

func TestGenerateIsDeterministicAndDecodable(t *testing.T) {
	source := filepath.Join(t.TempDir(), "source.png")
	sourceFile, err := os.Create(source)
	if err != nil {
		t.Fatal(err)
	}
	portrait := image.NewNRGBA(image.Rect(0, 0, 64, 96))
	for y := 0; y < 96; y++ {
		for x := 0; x < 64; x++ {
			portrait.SetNRGBA(x, y, color.NRGBA{R: uint8(x), G: uint8(y), B: 200, A: 255})
		}
	}
	if err := png.Encode(sourceFile, portrait); err != nil {
		t.Fatal(err)
	}
	if err := sourceFile.Close(); err != nil {
		t.Fatal(err)
	}
	first := filepath.Join(t.TempDir(), "first.ico")
	second := filepath.Join(t.TempDir(), "second.ico")
	preview := filepath.Join(t.TempDir(), "preview.png")
	if err := generate(source, preview, first); err != nil {
		t.Fatalf("generate first icon: %v", err)
	}
	if err := generate(source, "", second); err != nil {
		t.Fatalf("generate second icon: %v", err)
	}
	previewImage, err := os.Open(preview)
	if err != nil {
		t.Fatal(err)
	}
	decodedPreview, err := png.Decode(previewImage)
	_ = previewImage.Close()
	if err != nil {
		t.Fatalf("decode preview: %v", err)
	}
	if decodedPreview.Bounds().Dx() != 64 || decodedPreview.Bounds().Dy() != 64 {
		t.Fatalf("preview is not the top square crop: bounds=%v err=%v", decodedPreview.Bounds(), err)
	}
	for _, point := range []image.Point{{X: 0, Y: 0}, {X: 17, Y: 31}, {X: 63, Y: 63}} {
		got := color.NRGBAModel.Convert(decodedPreview.At(point.X, point.Y)).(color.NRGBA)
		want := portrait.NRGBAAt(point.X, point.Y)
		if got != want {
			t.Errorf("preview pixel %v = %#v, want top-crop pixel %#v", point, got, want)
		}
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
