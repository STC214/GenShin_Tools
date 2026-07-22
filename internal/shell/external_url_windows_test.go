package shell

import "testing"

func TestOpenExternalURLRejectsNonFufuTargetsBeforeShellExecute(t *testing.T) {
	for _, target := range []string{"http://fu1.fun/", "https://example.invalid/", "file:///C:/Windows/notepad.exe", "https://user@fu1.fun/", "https://fu1.fun:8443/"} {
		if err := openExternalURL(target); err == nil {
			t.Fatalf("unsafe external URL %q was accepted", target)
		}
	}
}
