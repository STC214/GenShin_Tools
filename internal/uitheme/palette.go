// Package uitheme resolves the shell's semantic colors without owning GDI objects.
package uitheme

const (
	PreferenceDark   = "dark"
	PreferenceSystem = "system"
)

// SystemColors contains the Windows colors used for high-contrast fallback.
type SystemColors struct {
	Window, WindowText, Highlight, HighlightText, ButtonFace, GrayText uint32
}

// Palette maps the original dark semantic colors used by the Win32 views to
// the colors selected for the current theme. Keeping this mapping centralized
// lets older pages participate in system and high-contrast themes while their
// text is progressively moved to resources.
type Palette struct {
	Dark     bool
	mapColor map[uint32]uint32
}

func RGB(r, g, b byte) uint32 { return uint32(r) | uint32(g)<<8 | uint32(b)<<16 }

func (p Palette) Map(color uint32) uint32 {
	if mapped, ok := p.mapColor[color]; ok {
		return mapped
	}
	return color
}

func Resolve(preference string, highContrast, appsUseLightTheme bool, system SystemColors) Palette {
	if highContrast {
		return highContrastPalette(system)
	}
	if preference == PreferenceSystem && appsUseLightTheme {
		return lightPalette()
	}
	return darkPalette()
}

func semanticPalette(values [19]uint32, dark bool) Palette {
	legacy := [...]uint32{
		RGB(15, 17, 23), RGB(24, 27, 36), RGB(45, 51, 80), RGB(100, 132, 255), RGB(20, 23, 31),
		RGB(25, 29, 39), RGB(35, 40, 54), RGB(52, 66, 112), RGB(74, 48, 35), RGB(42, 139, 103),
		RGB(235, 238, 248), RGB(225, 229, 242), RGB(190, 197, 216), RGB(166, 174, 197), RGB(145, 154, 180),
		RGB(126, 136, 160), RGB(255, 126, 126), RGB(255, 170, 150), RGB(255, 205, 150),
	}
	mapping := make(map[uint32]uint32, len(legacy))
	for index, color := range legacy {
		mapping[color] = values[index]
	}
	return Palette{Dark: dark, mapColor: mapping}
}

func darkPalette() Palette {
	values := [19]uint32{
		RGB(15, 17, 23), RGB(24, 27, 36), RGB(45, 51, 80), RGB(100, 132, 255), RGB(20, 23, 31),
		RGB(25, 29, 39), RGB(35, 40, 54), RGB(52, 66, 112), RGB(74, 48, 35), RGB(42, 139, 103),
		RGB(235, 238, 248), RGB(225, 229, 242), RGB(190, 197, 216), RGB(166, 174, 197), RGB(145, 154, 180),
		RGB(126, 136, 160), RGB(255, 126, 126), RGB(255, 170, 150), RGB(255, 205, 150),
	}
	return semanticPalette(values, true)
}

func lightPalette() Palette {
	values := [19]uint32{
		RGB(247, 248, 252), RGB(237, 239, 246), RGB(218, 225, 249), RGB(63, 92, 214), RGB(241, 243, 248),
		RGB(255, 255, 255), RGB(231, 234, 242), RGB(211, 220, 249), RGB(255, 232, 219), RGB(211, 240, 228),
		RGB(24, 28, 38), RGB(38, 43, 56), RGB(65, 72, 91), RGB(82, 90, 112), RGB(99, 108, 132),
		RGB(112, 121, 143), RGB(178, 32, 48), RGB(174, 65, 42), RGB(142, 85, 15),
	}
	return semanticPalette(values, false)
}

func highContrastPalette(system SystemColors) Palette {
	values := [19]uint32{
		system.Window, system.ButtonFace, system.Window, system.Highlight, system.Window,
		system.Window, system.ButtonFace, system.ButtonFace, system.ButtonFace, system.ButtonFace,
		system.WindowText, system.WindowText, system.WindowText, system.WindowText, system.WindowText,
		system.GrayText, system.WindowText, system.WindowText, system.WindowText,
	}
	// Let Windows own title-bar and native-control rendering in high contrast;
	// a high-contrast scheme is not necessarily dark.
	return semanticPalette(values, false)
}
