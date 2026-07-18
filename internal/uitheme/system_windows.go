package uitheme

import (
	"golang.org/x/sys/windows/registry"

	"genshintools/internal/platform/win32"
)

// Current reads the small, bounded set of Windows settings needed to resolve
// the shell palette. Registry failures safely retain the dark palette.
func Current(preference string) Palette {
	light := false
	if key, err := registry.OpenKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Themes\Personalize`, registry.QUERY_VALUE); err == nil {
		if value, _, readErr := key.GetIntegerValue("AppsUseLightTheme"); readErr == nil {
			light = value != 0
		}
		_ = key.Close()
	}
	return Resolve(preference, win32.HighContrastEnabled(), light, SystemColors{
		Window:        win32.SystemColor(win32.COLOR_WINDOW),
		WindowText:    win32.SystemColor(win32.COLOR_WINDOWTEXT),
		Highlight:     win32.SystemColor(win32.COLOR_HIGHLIGHT),
		HighlightText: win32.SystemColor(win32.COLOR_HIGHLIGHTTEXT),
		ButtonFace:    win32.SystemColor(win32.COLOR_BTNFACE),
		GrayText:      win32.SystemColor(win32.COLOR_GRAYTEXT),
	})
}
