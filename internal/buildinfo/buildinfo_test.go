package buildinfo

import (
	"strings"
	"testing"
)

func TestCurrentIsComplete(t *testing.T) {
	info := Current()
	if info.Product != "Genshin Tools" {
		t.Fatalf("Product = %q", info.Product)
	}
	if info.Version == "" || info.Commit == "" || info.BuildTimeUTC == "" || info.Configuration == "" {
		t.Fatalf("incomplete build identity: %+v", info)
	}
	if !strings.Contains(info.Platform, "/") {
		t.Fatalf("Platform = %q", info.Platform)
	}
}

func TestInfoString(t *testing.T) {
	info := Info{
		Product:       "Genshin Tools",
		Version:       "1.2.3",
		Commit:        "abcdef",
		Configuration: "release",
		Platform:      "windows/amd64",
	}
	got := info.String()
	for _, want := range []string{"Genshin Tools", "1.2.3", "abcdef", "release", "windows/amd64"} {
		if !strings.Contains(got, want) {
			t.Fatalf("String() = %q, missing %q", got, want)
		}
	}
}
