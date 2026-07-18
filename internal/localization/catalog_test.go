package localization

import (
	"reflect"
	"regexp"
	"testing"
)

var formatDirective = regexp.MustCompile(`%[-+# 0]*(?:\[[0-9]+\])?[0-9]*(?:\.[0-9]+)?[bcdoOqxXUeEfFgGspvTt]`)

func TestLanguageTablesHaveIdenticalKeys(t *testing.T) {
	for key := range enUS {
		if _, ok := zhCN[key]; !ok {
			t.Fatalf("zh-CN is missing %q", key)
		}
	}
	for key := range zhCN {
		if _, ok := enUS[key]; !ok {
			t.Fatalf("en-US is missing %q", key)
		}
	}
}

func TestLanguageTablesHaveMatchingFormatDirectives(t *testing.T) {
	for key, english := range enUS {
		englishDirectives := formatDirective.FindAllString(english, -1)
		chineseDirectives := formatDirective.FindAllString(zhCN[key], -1)
		if !reflect.DeepEqual(englishDirectives, chineseDirectives) {
			t.Fatalf("format directives for %q differ: en=%v zh=%v", key, englishDirectives, chineseDirectives)
		}
	}
}

func TestResolveSystemLanguage(t *testing.T) {
	if got := Resolve(System, "zh-Hans-CN"); got != ZH {
		t.Fatalf("Simplified Chinese resolved to %q", got)
	}
	if got := Resolve(System, "zh-TW"); got != EN {
		t.Fatalf("unsupported Traditional Chinese should fall back to English, got %q", got)
	}
	if got := Resolve(System, "en-US"); got != EN {
		t.Fatalf("English resolved to %q", got)
	}
}

func TestMissingKeyFallsBackToKey(t *testing.T) {
	if got := New(ZH, "").Text("missing.key"); got != "missing.key" {
		t.Fatalf("missing key = %q", got)
	}
}
