// Package upstreamaudit classifies read-only upstream changes without importing
// or executing upstream code or artifacts.
package upstreamaudit

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
	"time"
)

const maxLockBytes = 64 << 10

var shaPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)

type Lock struct {
	SchemaVersion int    `json:"schemaVersion"`
	Repository    string `json:"repository"`
	Owner         string `json:"owner"`
	Name          string `json:"name"`
	Branch        string `json:"branch"`
	Commit        string `json:"commit"`
	CommitTimeUTC string `json:"commitTimeUtc"`
	CheckedAtUTC  string `json:"checkedAtUtc"`
	ScopePolicy   string `json:"scopePolicy"`
	Mode          string `json:"mode"`
	Notes         string `json:"notes"`
}

func LoadLock(reader io.Reader) (Lock, error) {
	data, err := io.ReadAll(io.LimitReader(reader, maxLockBytes+1))
	if err != nil {
		return Lock{}, err
	}
	if len(data) > maxLockBytes {
		return Lock{}, errors.New("upstream lock exceeds 64 KiB")
	}
	var lock Lock
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&lock); err != nil {
		return Lock{}, fmt.Errorf("decode upstream lock: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return Lock{}, errors.New("upstream lock contains trailing JSON")
	}
	if err := lock.Validate(); err != nil {
		return Lock{}, err
	}
	return lock, nil
}

func (lock Lock) Validate() error {
	if lock.SchemaVersion != 1 || lock.Mode != "audit-only" || lock.ScopePolicy != "scope-v2" {
		return errors.New("unsupported upstream lock schema, mode, or scope policy")
	}
	if lock.Owner != "FufuLauncher" || lock.Name != "FufuLauncher" || lock.Branch != "master" {
		return errors.New("upstream lock repository identity is not allowed")
	}
	wantRepository := "https://github.com/" + lock.Owner + "/" + lock.Name + ".git"
	if lock.Repository != wantRepository || !shaPattern.MatchString(lock.Commit) {
		return errors.New("upstream lock repository URL or commit is invalid")
	}
	if _, err := time.Parse(time.RFC3339, lock.CommitTimeUTC); err != nil {
		return errors.New("upstream lock commitTimeUtc is invalid")
	}
	if _, err := time.Parse(time.RFC3339, lock.CheckedAtUTC); err != nil {
		return errors.New("upstream lock checkedAtUtc is invalid")
	}
	return nil
}

type Classification string

const (
	InScope        Classification = "in_scope"
	Excluded       Classification = "excluded"
	ReviewRequired Classification = "review_required"
	DependencyRisk Classification = "dependency_risk"
)

type FileChange struct {
	Commit   string `json:"commit"`
	Path     string `json:"path"`
	Status   string `json:"status"`
	Patch    string `json:"patch,omitempty"`
	Previous string `json:"previousPath,omitempty"`
	Added    int    `json:"additions"`
	Deleted  int    `json:"deletions"`
}

type Change struct {
	FileChange
	Module         string         `json:"module"`
	Classification Classification `json:"classification"`
	Confidence     string         `json:"confidence"`
	Rules          []string       `json:"rules"`
}

type markerRule struct {
	name    string
	markers []string
}

var inScopeRules = []markerRule{
	{"self-update", []string{"updatefufulauncher", "update.json", "setup.iss", "appversion", "assemblyversion", "fileversion"}},
	{"game-launch", []string{"gamelaunch", "launcher", "launchsetting", "gamepath", "gameservice"}},
	{"resources", []string{"genshindownloader", "download", "manifest", "sophon", "predownload", "repair", "verify"}},
	{"server", []string{"server", "bilibili", "channel", "sdk"}},
	{"input", []string{"autoclick", "sendinput", "keyboardhook", "mousehook", "hotkey"}},
	{"media", []string{"screenshot", "capture", "overlay", "fps", "gpu"}},
	{"local-enhancement", []string{"hdr", "bettergi", "shortcut", "startupsound"}},
	{"plugins", []string{"plugin", "inject", "module"}},
	{"shell", []string{"tray", "window", "theme", "language", "update"}},
}

var excludedMarkers = []string{
	"account", "login", "cookie", "token", "qrcode", "geetest", "securityweb",
	"checkin", "daily_note", "dailynote", "hoyolab", "mihoyo", "bbs", "community",
	"gacha", "uigf", "achievement", "inventory", "travelerdiary", "playerrole",
	"news", "contentfeed", "help", "calculator", "datacenter", "data-center",
	"browser", "webview", "additionalprogram", "toolbox", "controlpanel",
}

var reviewMarkers = []string{
	"http://", "https://", "api", "protobuf", ".proto", "schema", "registry",
	"regedit", "runas", "administrator", "elevat", "inject", "certificate",
	"publickey", "signature", "license", "packages.lock", "package.json", "csproj",
}

var binaryExtensions = []string{".dll", ".exe", ".zip", ".7z", ".msi", ".pdb", ".cer", ".pfx", ".bin"}

func Classify(files []FileChange) []Change {
	result := make([]Change, 0, len(files))
	for _, file := range files {
		result = append(result, classifyFile(file))
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Path != result[j].Path {
			return result[i].Path < result[j].Path
		}
		return result[i].Commit < result[j].Commit
	})
	return result
}

func classifyFile(file FileChange) Change {
	text := strings.ToLower(strings.Join([]string{file.Path, file.Previous, file.Patch}, "\n"))
	rules := make([]string, 0, 4)
	module, inScope := moduleFor(text)
	excluded := containsAny(text, excludedMarkers)
	review := containsAny(text, reviewMarkers) || hasSuffixFold(file.Path, binaryExtensions) || file.Status == "renamed" || file.Status == "removed"
	if inScope {
		rules = append(rules, "in-scope:"+module)
	}
	if excluded {
		rules = append(rules, "permanent-exclusion")
	}
	if review {
		rules = append(rules, "manual-review")
	}
	classification, confidence := ReviewRequired, "low"
	switch {
	case inScope && excluded:
		classification, confidence = DependencyRisk, "high"
	case review:
		classification, confidence = ReviewRequired, "high"
	case excluded:
		classification, confidence = Excluded, "high"
	case inScope:
		classification, confidence = InScope, "medium"
	default:
		rules = append(rules, "unrecognized-change")
	}
	if module == "" {
		module = "unknown"
	}
	sort.Strings(rules)
	return Change{FileChange: file, Module: module, Classification: classification, Confidence: confidence, Rules: rules}
}

func moduleFor(text string) (string, bool) {
	for _, rule := range inScopeRules {
		if containsAny(text, rule.markers) {
			return rule.name, true
		}
	}
	return "", false
}

func containsAny(value string, markers []string) bool {
	for _, marker := range markers {
		if strings.Contains(value, marker) {
			return true
		}
	}
	return false
}

func hasSuffixFold(value string, suffixes []string) bool {
	value = strings.ToLower(value)
	for _, suffix := range suffixes {
		if strings.HasSuffix(value, suffix) {
			return true
		}
	}
	return false
}
