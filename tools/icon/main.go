// Command icon crops the configured artwork and generates a deterministic
// multi-size Windows application icon.
package main

import (
	"bytes"
	"encoding/binary"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
	"path/filepath"
)

var sizes = []int{16, 24, 32, 48, 64, 128, 256}

type iconImage struct {
	size int
	png  []byte
}

func main() {
	source := flag.String("source", "01.png", "source artwork path")
	preview := flag.String("preview", filepath.FromSlash("assets/app-icon.png"), "square PNG preview path")
	output := flag.String("output", filepath.FromSlash("assets/app.ico"), "output ICO path")
	flag.Parse()
	if err := generate(*source, *preview, *output); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func generate(source, preview, output string) error {
	square, err := loadTopSquare(source)
	if err != nil {
		return err
	}
	if preview != "" {
		if err := writePNG(preview, square); err != nil {
			return fmt.Errorf("write square icon preview: %w", err)
		}
	}

	images := make([]iconImage, 0, len(sizes))
	for _, size := range sizes {
		var encoded bytes.Buffer
		if err := png.Encode(&encoded, resizeArea(square, size)); err != nil {
			return fmt.Errorf("encode %dx%d icon: %w", size, size, err)
		}
		images = append(images, iconImage{size: size, png: encoded.Bytes()})
	}

	var ico bytes.Buffer
	_ = binary.Write(&ico, binary.LittleEndian, uint16(0))
	_ = binary.Write(&ico, binary.LittleEndian, uint16(1))
	_ = binary.Write(&ico, binary.LittleEndian, uint16(len(images)))
	offset := 6 + len(images)*16
	for _, item := range images {
		width, height := byte(item.size), byte(item.size)
		if item.size == 256 {
			width, height = 0, 0
		}
		_ = ico.WriteByte(width)
		_ = ico.WriteByte(height)
		_ = ico.WriteByte(0)
		_ = ico.WriteByte(0)
		_ = binary.Write(&ico, binary.LittleEndian, uint16(1))
		_ = binary.Write(&ico, binary.LittleEndian, uint16(32))
		_ = binary.Write(&ico, binary.LittleEndian, uint32(len(item.png)))
		_ = binary.Write(&ico, binary.LittleEndian, uint32(offset))
		offset += len(item.png)
	}
	for _, item := range images {
		_, _ = ico.Write(item.png)
	}
	if err := os.MkdirAll(filepath.Dir(output), 0o755); err != nil {
		return fmt.Errorf("create icon directory: %w", err)
	}
	if err := os.WriteFile(output, ico.Bytes(), 0o644); err != nil {
		return fmt.Errorf("write icon: %w", err)
	}
	return nil
}

func loadTopSquare(path string) (*image.NRGBA, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open icon source: %w", err)
	}
	decoded, err := png.Decode(file)
	closeErr := file.Close()
	if err != nil || closeErr != nil {
		return nil, fmt.Errorf("decode icon source: %w", errorsJoin(err, closeErr))
	}
	bounds := decoded.Bounds()
	side := min(bounds.Dx(), bounds.Dy())
	if side <= 0 {
		return nil, fmt.Errorf("icon source has empty bounds")
	}
	left := bounds.Min.X + (bounds.Dx()-side)/2
	square := image.NewNRGBA(image.Rect(0, 0, side, side))
	draw.Draw(square, square.Bounds(), decoded, image.Pt(left, bounds.Min.Y), draw.Src)
	return square, nil
}

func resizeArea(source image.Image, size int) *image.NRGBA {
	bounds := source.Bounds()
	result := image.NewNRGBA(image.Rect(0, 0, size, size))
	for y := 0; y < size; y++ {
		y0 := bounds.Min.Y + y*bounds.Dy()/size
		y1 := bounds.Min.Y + (y+1)*bounds.Dy()/size
		if y1 <= y0 {
			y1 = y0 + 1
		}
		for x := 0; x < size; x++ {
			x0 := bounds.Min.X + x*bounds.Dx()/size
			x1 := bounds.Min.X + (x+1)*bounds.Dx()/size
			if x1 <= x0 {
				x1 = x0 + 1
			}
			var red, green, blue, alpha, count uint64
			for sy := y0; sy < y1; sy++ {
				for sx := x0; sx < x1; sx++ {
					pixel := color.NRGBAModel.Convert(source.At(sx, sy)).(color.NRGBA)
					red += uint64(pixel.R)
					green += uint64(pixel.G)
					blue += uint64(pixel.B)
					alpha += uint64(pixel.A)
					count++
				}
			}
			result.SetNRGBA(x, y, color.NRGBA{R: uint8(red / count), G: uint8(green / count), B: uint8(blue / count), A: uint8(alpha / count)})
		}
	}
	return result
}

func writePNG(path string, value image.Image) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	encodeErr := png.Encode(file, value)
	return errorsJoin(encodeErr, file.Close())
}

func errorsJoin(values ...error) error {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}
