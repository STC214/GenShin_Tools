package uitheme

import "testing"

func TestSystemLightPaletteChangesBackgroundAndText(t *testing.T) {
	dark := Resolve(PreferenceDark, false, true, SystemColors{})
	light := Resolve(PreferenceSystem, false, true, SystemColors{})
	if dark.Map(RGB(15, 17, 23)) == light.Map(RGB(15, 17, 23)) {
		t.Fatal("light theme did not change the background")
	}
	if light.Dark {
		t.Fatal("light system palette reported dark")
	}
}

func TestDarkPreferenceIgnoresSystemLightTheme(t *testing.T) {
	got := Resolve(PreferenceDark, false, true, SystemColors{})
	if !got.Dark || got.Map(RGB(15, 17, 23)) != RGB(15, 17, 23) {
		t.Fatal("explicit dark preference was not preserved")
	}
}

func TestHighContrastOverridesPreference(t *testing.T) {
	system := SystemColors{Window: 1, WindowText: 2, Highlight: 3, HighlightText: 4, ButtonFace: 5, GrayText: 6}
	got := Resolve(PreferenceSystem, true, true, system)
	if got.Dark {
		t.Fatal("high contrast must leave native control theming to Windows")
	}
	if got.Map(RGB(15, 17, 23)) != system.Window {
		t.Fatal("high contrast did not use COLOR_WINDOW")
	}
	if got.Map(RGB(235, 238, 248)) != system.WindowText {
		t.Fatal("high contrast text did not use COLOR_WINDOWTEXT")
	}
}
