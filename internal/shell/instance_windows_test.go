package shell

import "testing"

func TestScopedShellName(t *testing.T) {
	const base = `Local\GenshinTools.Singleton.S02`

	t.Setenv("GENSHINTOOLS_S02_INSTANCE_SUFFIX", "test-42")
	if got := scopedShellName(base); got != base+".test-42" {
		t.Fatalf("scoped shell name = %q", got)
	}

	for _, invalid := range []string{"contains space", `path\escape`, "dot.name", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"} {
		t.Setenv("GENSHINTOOLS_S02_INSTANCE_SUFFIX", invalid)
		if got := scopedShellName(base); got != base {
			t.Fatalf("invalid suffix %q changed shell name to %q", invalid, got)
		}
	}
}
