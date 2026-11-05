package injection

import "testing"

func TestCheckGameVersionRule(t *testing.T) {
	for _, test := range []struct {
		rule, version string
		accept        bool
	}{
		{"", "legacy-audit", true}, {"7.2", "7.2.0", true}, {"7.2.x", "7.2.1", true},
		{"7.1", "7.2.0", false}, {"7.1,7.2", "7.2.0", true}, {">=7.1 & <7.3", "7.2.0", true},
		{">=7.1 & <7.2", "7.2.0", false}, {"7.1~7.2", "7.2.99", true}, {"7.3+", "7.2.0", false},
		{"any except 7.2", "7.2.0", false}, {"!7.1", "7.2.0", true},
		{"通用，不支持7.2", "7.2.0", false}, {"7.1或者7.2", "7.2.0", true},
		{"≥7.1且≤7.2", "7.2.0", true}, {"v7.2", "7.2.0", true},
		{"7.2-", "7.3.0", true}, {"-7.2", "7.1.0", true},
		{"7.2", "unknown", false}, {"7.3~7.1", "7.2.0", false},
		{"7.2,broken", "7.2.0", false}, {"7.2evil", "7.2.0", false},
		{"7.*.2", "7.2.0", false}, {"7.2.0.0.0", "7.2.0", false}, {",;&", "7.2.0", false},
	} {
		t.Run(test.rule+"/"+test.version, func(t *testing.T) {
			if err := CheckGameVersionRule(test.rule, test.version); (err == nil) != test.accept {
				t.Fatalf("accept=%v error=%v", test.accept, err)
			}
		})
	}
}
