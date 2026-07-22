package plugins

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

var fufuStoreIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

const (
	maxCatalogBytes     = 8 << 20
	FufuStoreBaseURL    = "https://fu1.fun"
	FufuStoreRepository = "https://github.com/FufuLauncher/FufuLauncher"
	fufuStoreListPath   = "/api/v1/plugins/list"
	fufuStorePageSize   = 100
	maxFufuStorePlugins = 5000
)

type Config struct {
	SafeMode bool `json:"safeMode"`
	// CatalogURL is retained only so existing schema-v9 settings remain readable.
	// Normalized always clears it: the store source is fixed to FufuLauncher.
	CatalogURL string `json:"catalogUrl,omitempty"`
	Category   string `json:"category"`
	Search     string `json:"search"`
	Sort       string `json:"sort"`
	Page       int    `json:"page"`
	PageSize   int    `json:"pageSize"`
}

func DefaultConfig() Config {
	return Config{SafeMode: true, Sort: "popular", Page: 1, PageSize: 20}
}

func (config Config) Normalized() (Config, error) {
	config.CatalogURL = ""
	config.Category = strings.TrimSpace(config.Category)
	config.Search = strings.TrimSpace(config.Search)
	config.Sort = strings.TrimSpace(config.Sort)
	if config.Category != "" && !containsExact([]string{"utility", "gameplay", "visuals", "other"}, config.Category) {
		return Config{}, errors.New("plugin catalog category is invalid")
	}
	if len([]rune(config.Search)) > 128 || strings.ContainsAny(config.Search, "\x00\r\n") {
		return Config{}, errors.New("plugin search is limited to 128 single-line characters")
	}
	if !containsExact([]string{"popular", "newest", "rating", "name"}, config.Sort) {
		return Config{}, errors.New("plugin catalog sort mode is invalid")
	}
	if config.Page < 1 || config.Page > 100_000 || config.PageSize < 1 || config.PageSize > 100 {
		return Config{}, errors.New("plugin catalog page/pageSize is outside limits")
	}
	return config, nil
}

// Catalog is a normalized local cache of the FufuLauncher store response. It
// is not a separately hosted catalog protocol.
type Catalog struct {
	SchemaVersion int           `json:"schemaVersion"`
	ID            string        `json:"id"`
	Name          string        `json:"name"`
	SourceURL     string        `json:"sourceUrl"`
	License       string        `json:"license"`
	GeneratedUTC  string        `json:"generatedUtc"`
	Plugins       []CatalogItem `json:"plugins"`
}

type CatalogItem struct {
	ID                string           `json:"id"`
	Name              string           `json:"name"`
	Developer         string           `json:"developer"`
	Description       string           `json:"description"`
	LongDescription   string           `json:"longDescription,omitempty"`
	Version           string           `json:"version"`
	Category          string           `json:"category"`
	Tags              []string         `json:"tags,omitempty"`
	Capabilities      []string         `json:"capabilities"`
	SourceURL         string           `json:"sourceUrl"`
	License           string           `json:"license"`
	PackageURL        string           `json:"packageUrl"`
	PackageSize       int64            `json:"packageSize"`
	PackageSHA256     string           `json:"packageSha256"`
	LuaInstallURL     string           `json:"luaInstallUrl,omitempty"`
	LuaUninstallURL   string           `json:"luaUninstallUrl,omitempty"`
	LuaSHA256         string           `json:"luaSha256,omitempty"`
	DLLFileName       string           `json:"dllFileName,omitempty"`
	MinimumAppVersion string           `json:"minimumAppVersion,omitempty"`
	Visibility        string           `json:"visibility,omitempty"`
	UpdateType        string           `json:"updateType,omitempty"`
	Dependencies      []FufuDependency `json:"dependencies,omitempty"`
	Rating            float64          `json:"rating,omitempty"`
	Downloads         int64            `json:"downloads,omitempty"`
	UpdatedUTC        string           `json:"updatedUtc"`
}

type FufuDependency struct {
	PluginName     string `json:"pluginName,omitempty"`
	ProjectName    string `json:"projectName,omitempty"`
	ProjectVersion string `json:"projectVersion,omitempty"`
}

type CatalogPage struct {
	Items      []CatalogItem
	Page       int
	PageSize   int
	Total      int
	TotalPages int
}

type fufuStoreResponse struct {
	Retcode int           `json:"retcode"`
	Message string        `json:"message"`
	Data    fufuStoreData `json:"data"`
}

type fufuStoreData struct {
	Total   int          `json:"total"`
	Page    int          `json:"page"`
	Plugins []fufuPlugin `json:"plugins"`
}

type fufuPlugin struct {
	ID                string   `json:"id"`
	Name              string   `json:"name"`
	Developer         string   `json:"developer"`
	Description       string   `json:"description"`
	LongDescription   string   `json:"long_description"`
	Version           string   `json:"version"`
	Category          string   `json:"category"`
	Tags              []string `json:"tags"`
	Downloads         int64    `json:"downloads"`
	SizeBytes         int64    `json:"size_bytes"`
	MinimumAppVersion string   `json:"min_app_version"`
	UpdatedAt         string   `json:"updated_at"`
	LuaInstallURL     string   `json:"lua_install_url"`
	LuaUninstallURL   string   `json:"lua_uninstall_url"`
	DownloadURL       string   `json:"download_url"`
	FileHash          string   `json:"file_hash"`
	LuaHash           string   `json:"lua_hash"`
	DLLFileName       string   `json:"dll_file_name"`
	Visibility        string   `json:"visibility"`
	UpdateType        string   `json:"update_type"`
	Dependencies      []struct {
		PluginName     string `json:"plugin_name"`
		ProjectName    string `json:"project_name"`
		ProjectVersion string `json:"project_version"`
	} `json:"dependencies"`
}

func LoadCatalog(path string) (Catalog, error) {
	data, err := readFileBounded(path, maxCatalogBytes)
	if err != nil {
		return Catalog{}, err
	}
	return decodeCatalog(data)
}

// SyncCatalog downloads the current fixed FufuLauncher store and atomically
// replaces the local normalized cache only after every response is validated.
func SyncCatalog(ctx context.Context, client *http.Client, destination string) (Catalog, error) {
	return syncCatalog(ctx, client, FufuStoreBaseURL, destination)
}

func syncCatalog(ctx context.Context, client *http.Client, baseURL, destination string) (Catalog, error) {
	if err := validateHTTPSURL(baseURL, "Fufu store base URL"); err != nil {
		return Catalog{}, err
	}
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	items := make([]CatalogItem, 0, fufuStorePageSize)
	seen := make(map[string]bool)
	for page := 1; ; page++ {
		response, err := fetchFufuStorePage(ctx, client, baseURL, page)
		if err != nil {
			return Catalog{}, err
		}
		for _, upstream := range response.Data.Plugins {
			item := mapFufuPlugin(upstream, baseURL)
			if err := validateFufuCatalogItem(item, baseURL); err != nil {
				return Catalog{}, fmt.Errorf("Fufu store plugin %q: %w", upstream.ID, err)
			}
			if seen[item.ID] {
				return Catalog{}, fmt.Errorf("duplicate Fufu store plugin %q", item.ID)
			}
			seen[item.ID] = true
			items = append(items, item)
		}
		if len(items) > maxFufuStorePlugins {
			return Catalog{}, errors.New("Fufu store exceeds 5000 entries")
		}
		if len(response.Data.Plugins) == 0 || len(items) >= response.Data.Total {
			break
		}
		if page >= (maxFufuStorePlugins+fufuStorePageSize-1)/fufuStorePageSize {
			return Catalog{}, errors.New("Fufu store pagination exceeded safety limit")
		}
	}
	source := strings.TrimRight(baseURL, "/") + fufuStoreListPath
	catalog := Catalog{SchemaVersion: 1, ID: "fufu-launcher", Name: "FufuLauncher Plugin Store", SourceURL: source, License: "FufuLauncher upstream service; plugin licenses remain with their authors", GeneratedUTC: time.Now().UTC().Format(time.RFC3339), Plugins: items}
	data, err := json.MarshalIndent(catalog, "", "  ")
	if err != nil {
		return Catalog{}, err
	}
	if len(data) > maxCatalogBytes {
		return Catalog{}, errors.New("normalized Fufu store cache exceeds 8 MiB")
	}
	if err := atomicWrite(destination, append(data, '\n')); err != nil {
		return Catalog{}, err
	}
	return catalog, nil
}

func fetchFufuStorePage(ctx context.Context, client *http.Client, baseURL string, page int) (fufuStoreResponse, error) {
	endpoint, err := url.Parse(strings.TrimRight(baseURL, "/") + fufuStoreListPath)
	if err != nil {
		return fufuStoreResponse{}, err
	}
	query := endpoint.Query()
	query.Set("sort", "popular")
	query.Set("page", strconv.Itoa(page))
	query.Set("page_size", strconv.Itoa(fufuStorePageSize))
	query.Set("lang", "zh-CN")
	endpoint.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return fufuStoreResponse{}, err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "GenshinTools-FufuStoreAdapter/1")
	httpResponse, err := client.Do(request)
	if err != nil {
		return fufuStoreResponse{}, err
	}
	defer httpResponse.Body.Close()
	if httpResponse.Request == nil || httpResponse.Request.URL == nil || !sameURLOrigin(baseURL, httpResponse.Request.URL.String()) {
		return fufuStoreResponse{}, errors.New("Fufu store redirect changed the trusted HTTPS origin")
	}
	if httpResponse.StatusCode != http.StatusOK {
		return fufuStoreResponse{}, fmt.Errorf("Fufu store HTTP status %d", httpResponse.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(httpResponse.Body, maxCatalogBytes+1))
	if err != nil {
		return fufuStoreResponse{}, err
	}
	if len(data) > maxCatalogBytes {
		return fufuStoreResponse{}, errors.New("Fufu store response exceeds 8 MiB")
	}
	var response fufuStoreResponse
	if err := json.Unmarshal(bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF}), &response); err != nil {
		return fufuStoreResponse{}, fmt.Errorf("decode Fufu store response: %w", err)
	}
	if response.Retcode != 0 {
		return fufuStoreResponse{}, fmt.Errorf("Fufu store retcode %d: %s", response.Retcode, strings.TrimSpace(response.Message))
	}
	if response.Data.Total < 0 || response.Data.Total > maxFufuStorePlugins || response.Data.Page != page || len(response.Data.Plugins) > fufuStorePageSize {
		return fufuStoreResponse{}, errors.New("Fufu store pagination metadata is invalid")
	}
	return response, nil
}

func mapFufuPlugin(item fufuPlugin, baseURL string) CatalogItem {
	packageURL := strings.TrimSpace(item.DownloadURL)
	if packageURL == "" && fufuStoreIDPattern.MatchString(item.ID) {
		packageURL = strings.TrimRight(baseURL, "/") + "/plugins/files/" + url.PathEscape(item.ID) + ".zip"
	}
	sourceURL := FufuStoreRepository
	for _, field := range strings.Fields(item.LongDescription) {
		if index := strings.Index(field, "https://"); index >= 0 {
			field = field[index:]
		}
		candidate := strings.Trim(field, `<>[](){}，。；;、"'`)
		if validateHTTPSURL(candidate, "sourceUrl") == nil {
			sourceURL = candidate
			break
		}
	}
	dependencies := make([]FufuDependency, 0, len(item.Dependencies))
	for _, dependency := range item.Dependencies {
		dependencies = append(dependencies, FufuDependency{PluginName: dependency.PluginName, ProjectName: dependency.ProjectName, ProjectVersion: dependency.ProjectVersion})
	}
	return CatalogItem{
		ID: item.ID, Name: item.Name, Developer: item.Developer, Description: item.Description,
		LongDescription: item.LongDescription, Version: item.Version, Category: item.Category,
		Tags: item.Tags, Capabilities: []string{"game.plugin"}, SourceURL: sourceURL,
		License: "UNSPECIFIED-FUFU-STORE", PackageURL: packageURL,
		PackageSize: item.SizeBytes, PackageSHA256: strings.ToLower(item.FileHash),
		LuaInstallURL: item.LuaInstallURL, LuaUninstallURL: item.LuaUninstallURL,
		LuaSHA256: strings.ToLower(item.LuaHash), DLLFileName: item.DLLFileName,
		MinimumAppVersion: item.MinimumAppVersion, Visibility: item.Visibility,
		UpdateType: item.UpdateType, Downloads: item.Downloads, UpdatedUTC: item.UpdatedAt,
		Dependencies: dependencies,
	}
}

func QueryCatalog(catalog Catalog, config Config) (CatalogPage, error) {
	config, err := config.Normalized()
	if err != nil {
		return CatalogPage{}, err
	}
	items := make([]CatalogItem, 0, len(catalog.Plugins))
	search := strings.ToLower(config.Search)
	for _, item := range catalog.Plugins {
		if config.Category != "" && item.Category != config.Category {
			continue
		}
		haystack := strings.ToLower(item.ID + " " + item.Name + " " + item.Developer + " " + item.Description + " " + item.LongDescription + " " + strings.Join(item.Tags, " "))
		if search != "" && !strings.Contains(haystack, search) {
			continue
		}
		items = append(items, item)
	}
	sort.SliceStable(items, func(left, right int) bool {
		switch config.Sort {
		case "newest":
			return items[left].UpdatedUTC > items[right].UpdatedUTC
		case "rating":
			if items[left].Rating != items[right].Rating {
				return items[left].Rating > items[right].Rating
			}
		case "name":
			return strings.ToLower(items[left].Name) < strings.ToLower(items[right].Name)
		default:
			if items[left].Downloads != items[right].Downloads {
				return items[left].Downloads > items[right].Downloads
			}
		}
		return items[left].ID < items[right].ID
	})
	total := len(items)
	totalPages := max(1, (total+config.PageSize-1)/config.PageSize)
	if config.Page > totalPages {
		config.Page = totalPages
	}
	start := (config.Page - 1) * config.PageSize
	end := min(total, start+config.PageSize)
	return CatalogPage{Items: append([]CatalogItem(nil), items[start:end]...), Page: config.Page, PageSize: config.PageSize, Total: total, TotalPages: totalPages}, nil
}

func decodeCatalog(data []byte) (Catalog, error) {
	if len(data) > maxCatalogBytes {
		return Catalog{}, errors.New("plugin catalog exceeds 8 MiB")
	}
	var catalog Catalog
	decoder := json.NewDecoder(bytes.NewReader(bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF})))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&catalog); err != nil {
		return Catalog{}, err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return Catalog{}, errors.New("plugin catalog contains trailing JSON data")
	}
	if catalog.SchemaVersion != 1 || catalog.ID != "fufu-launcher" || catalog.Name != "FufuLauncher Plugin Store" || !sameURLOrigin(FufuStoreBaseURL, catalog.SourceURL) {
		return Catalog{}, errors.New("plugin cache is not a FufuLauncher store snapshot")
	}
	if _, err := time.Parse(time.RFC3339, catalog.GeneratedUTC); err != nil {
		return Catalog{}, errors.New("plugin catalog generatedUtc is invalid")
	}
	if len(catalog.Plugins) > maxFufuStorePlugins {
		return Catalog{}, errors.New("plugin catalog exceeds 5000 entries")
	}
	seen := map[string]bool{}
	for _, item := range catalog.Plugins {
		if err := validateFufuCatalogItem(item, FufuStoreBaseURL); err != nil {
			return Catalog{}, fmt.Errorf("catalog plugin %q: %w", item.ID, err)
		}
		if seen[item.ID] {
			return Catalog{}, fmt.Errorf("duplicate catalog plugin %q", item.ID)
		}
		seen[item.ID] = true
	}
	return catalog, nil
}

func validateCatalogItem(item CatalogItem) error {
	manifest := Manifest{SchemaVersion: 1, ID: item.ID, Name: item.Name, Developer: item.Developer, Description: item.Description, Version: item.Version, Category: item.Category, Tags: item.Tags, Capabilities: item.Capabilities, SourceURL: item.SourceURL, License: item.License, ModuleFile: "module.json", Files: []PackageFile{{Path: "module.json", Size: 1, SHA256: strings.Repeat("0", 64)}}}
	if err := validateManifest(manifest, item.ID); err != nil {
		return err
	}
	if err := validateHTTPSURL(item.PackageURL, "packageUrl"); err != nil {
		return err
	}
	if item.PackageSize <= 0 || item.PackageSize > 256<<20 || !validSHA256(item.PackageSHA256) {
		return errors.New("package size or SHA-256 is invalid")
	}
	if item.LuaSHA256 != "" && !validSHA256(item.LuaSHA256) {
		return errors.New("Lua SHA-256 is invalid")
	}
	for _, scriptURL := range []string{item.LuaInstallURL, item.LuaUninstallURL} {
		if scriptURL != "" && validateHTTPSURL(scriptURL, "Lua URL") != nil {
			return errors.New("Lua URL is invalid")
		}
	}
	if item.Rating < 0 || item.Rating > 5 || item.Downloads < 0 {
		return errors.New("rating/download count is invalid")
	}
	if _, err := time.Parse(time.RFC3339, item.UpdatedUTC); err != nil {
		return errors.New("updatedUtc is invalid")
	}
	return nil
}

func validateFufuCatalogItem(item CatalogItem, origin string) error {
	if !fufuStoreIDPattern.MatchString(item.ID) || strings.TrimSpace(item.Name) == "" || strings.TrimSpace(item.Developer) == "" || strings.TrimSpace(item.Description) == "" {
		return errors.New("Fufu plugin identity is invalid")
	}
	if !versionPattern.MatchString(item.Version) || !containsExact([]string{"utility", "gameplay", "visuals", "other"}, item.Category) {
		return errors.New("Fufu plugin version or category is invalid")
	}
	if err := validateHTTPSURL(item.SourceURL, "sourceUrl"); err != nil {
		return err
	}
	if strings.TrimSpace(item.License) == "" {
		return errors.New("Fufu plugin license marker is missing")
	}
	if err := validateHTTPSURL(item.PackageURL, "packageUrl"); err != nil {
		return err
	}
	if item.PackageSize <= 0 || item.PackageSize > 256<<20 || !validSHA256(item.PackageSHA256) {
		return errors.New("package size or SHA-256 is invalid")
	}
	if item.LuaSHA256 != "" && !validSHA256(item.LuaSHA256) {
		return errors.New("Lua SHA-256 is invalid")
	}
	if item.Downloads < 0 {
		return errors.New("download count is invalid")
	}
	if len(item.Dependencies) > 64 {
		return errors.New("Fufu plugin dependency list is too large")
	}
	if _, err := time.Parse(time.RFC3339, item.UpdatedUTC); err != nil {
		return errors.New("updatedUtc is invalid")
	}
	if !sameURLOrigin(origin, item.PackageURL) {
		return errors.New("package URL is outside the FufuLauncher store origin")
	}
	for _, scriptURL := range []string{item.LuaInstallURL, item.LuaUninstallURL} {
		if scriptURL != "" && (validateHTTPSURL(scriptURL, "Lua URL") != nil || !sameURLOrigin(origin, scriptURL)) {
			return errors.New("Lua URL is outside the FufuLauncher store origin")
		}
	}
	return nil
}

func validSHA256(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 32
}

func sameURLOrigin(left, right string) bool {
	leftURL, leftErr := url.Parse(left)
	rightURL, rightErr := url.Parse(right)
	return leftErr == nil && rightErr == nil && strings.EqualFold(leftURL.Scheme, rightURL.Scheme) && strings.EqualFold(leftURL.Host, rightURL.Host)
}
