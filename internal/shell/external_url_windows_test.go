package shell

import "testing"

func TestOpenExternalURLRejectsNonFufuTargetsBeforeShellExecute(t *testing.T) {
	for _, target := range []string{"http://fu1.fun/", "https://example.invalid/", "file:///C:/Windows/notepad.exe", "https://user@fu1.fun/", "https://fu1.fun:8443/"} {
		if err := openExternalURL(target); err == nil {
			t.Fatalf("unsafe external URL %q was accepted", target)
		}
	}
}

func TestOpenInterceptionReleaseURLRejectsEveryUnpinnedTarget(t *testing.T) {
	for _, target := range []string{
		"http://github.com/oblitum/Interception/releases/tag/v1.0.1",
		"https://github.com/oblitum/Interception/releases/latest",
		"https://github.com/oblitum/Interception/releases/tag/v1.0.1?download=1",
		"https://example.invalid/",
	} {
		if err := openInterceptionReleaseURL(target); err == nil {
			t.Fatalf("untrusted Interception URL %q was accepted", target)
		}
	}
}
