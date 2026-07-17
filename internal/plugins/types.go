// Package plugins owns the script-free S10 plugin library, catalog and
// transactional package lifecycle. Plugin code is never loaded in this package.
package plugins

import (
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	ManifestSchemaVersion = 1
	StateSchemaVersion    = 1
	ConfigSchemaVersion   = 1
)

var (
	idPattern      = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)
	stagePattern   = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,127}$`)
	versionPattern = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?$`)
)

type Manifest struct {
	SchemaVersion int           `json:"schemaVersion"`
	ID            string        `json:"id"`
	Name          string        `json:"name"`
	Developer     string        `json:"developer"`
	Description   string        `json:"description"`
	Version       string        `json:"version"`
	Category      string        `json:"category"`
	Tags          []string      `json:"tags,omitempty"`
	Capabilities  []string      `json:"capabilities"`
	SourceURL     string        `json:"sourceUrl"`
	License       string        `json:"license"`
	ModuleFile    string        `json:"moduleFile"`
	ConfigSchema  string        `json:"configSchema,omitempty"`
	Files         []PackageFile `json:"files"`
}

type PackageFile struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

type Item struct {
	Manifest     Manifest
	Directory    string
	ModulePath   string
	ConfigPath   string
	SchemaPath   string
	Enabled      bool
	Order        int
	Alias        string
	AuditWarning string
}

func (item Item) DisplayName() string {
	if item.Alias != "" {
		return item.Alias
	}
	return item.Manifest.Name
}

func validateManifest(manifest Manifest, directoryID string) error {
	if manifest.SchemaVersion != ManifestSchemaVersion {
		return errors.New("unsupported plugin manifest schema")
	}
	if manifest.ID != directoryID || !idPattern.MatchString(manifest.ID) {
		return errors.New("plugin id does not match its directory")
	}
	if strings.TrimSpace(manifest.Name) == "" || len([]rune(manifest.Name)) > 96 {
		return errors.New("plugin name is required and limited to 96 characters")
	}
	if strings.TrimSpace(manifest.Developer) == "" || strings.TrimSpace(manifest.Description) == "" || strings.TrimSpace(manifest.License) == "" {
		return errors.New("plugin developer, description and license are required")
	}
	if !versionPattern.MatchString(manifest.Version) {
		return errors.New("plugin version must be SemVer")
	}
	if !containsExact([]string{"utility", "gameplay", "visuals", "other"}, manifest.Category) {
		return fmt.Errorf("unsupported plugin category %q", manifest.Category)
	}
	if len(manifest.Capabilities) == 0 || len(manifest.Capabilities) > 16 || len(manifest.Tags) > 32 {
		return errors.New("plugin must declare 1..16 capabilities and at most 32 tags")
	}
	for _, value := range append(append([]string(nil), manifest.Capabilities...), manifest.Tags...) {
		value = strings.ToLower(strings.TrimSpace(value))
		if value == "" || len(value) > 64 {
			return errors.New("plugin capability/tag is empty or too long")
		}
		if forbiddenCapability(value) {
			return fmt.Errorf("plugin capability/tag %q is outside project scope", value)
		}
	}
	if err := validateHTTPSURL(manifest.SourceURL, "sourceUrl"); err != nil {
		return err
	}
	if manifest.ModuleFile != "module.json" {
		return errors.New("plugin moduleFile must be module.json")
	}
	if manifest.ConfigSchema != "" && (filepath.Base(manifest.ConfigSchema) != manifest.ConfigSchema || manifest.ConfigSchema != "config.schema.json") {
		return errors.New("plugin configSchema must be config.schema.json")
	}
	if err := validatePackageFiles(manifest); err != nil {
		return err
	}
	return nil
}

func validateHTTPSURL(value, field string) error {
	parsed, err := url.Parse(value)
	if err != nil || !strings.EqualFold(parsed.Scheme, "https") || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" {
		return fmt.Errorf("plugin %s must be an HTTPS URL without credentials or fragment", field)
	}
	return nil
}

func forbiddenCapability(value string) bool {
	forbidden := []string{"account", "login", "cookie", "token", "credential", "gacha", "wish", "checkin", "sign-in", "bbs", "community", "news", "browser", "webview", "data-center", "calculator", "mihoyo", "hoyolab"}
	for _, item := range forbidden {
		if value == item || strings.HasPrefix(value, item+".") || strings.HasPrefix(value, item+"-") {
			return true
		}
	}
	return false
}

func containsExact(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}
