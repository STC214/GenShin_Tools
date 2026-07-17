// Command icon generates the deterministic multi-size Windows application icon.
package main

import (
	"bytes"
	"encoding/binary"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"
)

var sizes = []int{16, 24, 32, 48, 64, 128, 256}

type iconImage struct {
	size int
	png  []byte
}

func main() {
	output := flag.String("output", filepath.FromSlash("assets/app.ico"), "output ICO path")
	flag.Parse()
	if err := generate(*output); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func generate(output string) error {
	images := make([]iconImage, 0, len(sizes))
	for _, size := range sizes {
		var encoded bytes.Buffer
		if err := png.Encode(&encoded, render(size)); err != nil {
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
		width := byte(item.size)
		height := byte(item.size)
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

func render(size int) image.Image {
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	const samples = 4
	for py := 0; py < size; py++ {
		for px := 0; px < size; px++ {
			var r, g, b, a float64
			for sy := 0; sy < samples; sy++ {
				for sx := 0; sx < samples; sx++ {
					x := (float64(px)+(float64(sx)+0.5)/samples)/float64(size)*2 - 1
					y := (float64(py)+(float64(sy)+0.5)/samples)/float64(size)*2 - 1
					c := sample(x, y)
					r += float64(c.R)
					g += float64(c.G)
					b += float64(c.B)
					a += float64(c.A)
				}
			}
			divisor := float64(samples * samples)
			img.SetNRGBA(px, py, color.NRGBA{R: byte(r / divisor), G: byte(g / divisor), B: byte(b / divisor), A: byte(a / divisor)})
		}
	}
	return img
}

func sample(x, y float64) color.NRGBA {
	const corner = 0.30
	ax, ay := math.Abs(x), math.Abs(y)
	dx, dy := math.Max(ax-(1-corner), 0), math.Max(ay-(1-corner), 0)
	if math.Hypot(dx, dy) > corner || ax > 1 || ay > 1 {
		return color.NRGBA{}
	}

	// Dark indigo background with a restrained top-left highlight.
	t := clamp((x+y+2)/4, 0, 1)
	base := blend(color.NRGBA{R: 22, G: 25, B: 40, A: 255}, color.NRGBA{R: 47, G: 35, B: 83, A: 255}, t)
	highlight := clamp(1-math.Hypot(x+0.55, y+0.65), 0, 1) * 0.18
	base = blend(base, color.NRGBA{R: 92, G: 110, B: 255, A: 255}, highlight)

	// A cyan-violet orbit suggests motion without copying an existing game logo.
	radius := math.Hypot(x+0.05, y+0.02)
	if d := math.Abs(radius - 0.49); d < 0.105 {
		alpha := clamp((0.105-d)/0.035, 0, 1)
		orbit := blend(color.NRGBA{R: 90, G: 220, B: 255, A: 255}, color.NRGBA{R: 153, G: 104, B: 255, A: 255}, clamp((x+1)/2, 0, 1))
		base = blend(base, orbit, alpha)
	}

	// Four-point warm spark provides a readable center at small icon sizes.
	sx, sy := math.Abs(x-0.25), math.Abs(y+0.25)
	star := math.Min(sx*0.42+sy, sx+sy*0.42)
	if star < 0.13 {
		alpha := clamp((0.13-star)/0.045, 0, 1)
		base = blend(base, color.NRGBA{R: 255, G: 216, B: 112, A: 255}, alpha)
	}
	return base
}

func blend(a, b color.NRGBA, t float64) color.NRGBA {
	t = clamp(t, 0, 1)
	return color.NRGBA{
		R: uint8(float64(a.R)*(1-t) + float64(b.R)*t),
		G: uint8(float64(a.G)*(1-t) + float64(b.G)*t),
		B: uint8(float64(a.B)*(1-t) + float64(b.B)*t),
		A: uint8(float64(a.A)*(1-t) + float64(b.A)*t),
	}
}

func clamp(value, minimum, maximum float64) float64 {
	return math.Min(math.Max(value, minimum), maximum)
}
