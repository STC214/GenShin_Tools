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
	"os"
	"sort"
	"strings"
	"time"
)

const maxCatalogBytes = 8 << 20

type Config struct {
	SafeMode   bool   `json:"safeMode"`
	CatalogURL string `json:"catalogUrl"`
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
	config.CatalogURL = strings.TrimSpace(config.CatalogURL)
	config.Category = strings.TrimSpace(config.Category)
	config.Search = strings.TrimSpace(config.Search)
	config.Sort = strings.TrimSpace(config.Sort)
	if config.CatalogURL != "" {
		if err := validateHTTPSURL(config.CatalogURL, "catalogUrl"); err != nil {
			return Config{}, err
		}
	}
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
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	Developer     string   `json:"developer"`
	Description   string   `json:"description"`
	Version       string   `json:"version"`
	Category      string   `json:"category"`
	Tags          []string `json:"tags,omitempty"`
	Capabilities  []string `json:"capabilities"`
	SourceURL     string   `json:"sourceUrl"`
	License       string   `json:"license"`
	PackageURL    string   `json:"packageUrl"`
	PackageSize   int64    `json:"packageSize"`
	PackageSHA256 string   `json:"packageSha256"`
	Rating        float64  `json:"rating,omitempty"`
	Downloads     int64    `json:"downloads,omitempty"`
	UpdatedUTC    string   `json:"updatedUtc"`
}

type CatalogPage struct {
	Items      []CatalogItem
	Page       int
	PageSize   int
	Total      int
	TotalPages int
}

func LoadCatalog(path string) (Catalog, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Catalog{}, err
	}
	return decodeCatalog(data)
}

func SyncCatalog(ctx context.Context, client *http.Client, catalogURL, destination string) (Catalog, error) {
	if err := validateHTTPSURL(catalogURL, "catalogUrl"); err != nil {
		return Catalog{}, err
	}
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, catalogURL, nil)
	if err != nil {
		return Catalog{}, err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "GenshinTools-PluginCatalog/1")
	response, err := client.Do(request)
	if err != nil {
		return Catalog{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return Catalog{}, fmt.Errorf("plugin catalog HTTP status %d", response.StatusCode)
	}
	if response.ContentLength > maxCatalogBytes {
		return Catalog{}, errors.New("plugin catalog exceeds 8 MiB")
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxCatalogBytes+1))
	if err != nil {
		return Catalog{}, err
	}
	if len(data) > maxCatalogBytes {
		return Catalog{}, errors.New("plugin catalog exceeds 8 MiB")
	}
	catalog, err := decodeCatalog(data)
	if err != nil {
		return Catalog{}, err
	}
	if !sameURLOrigin(catalogURL, catalog.SourceURL) {
		return Catalog{}, errors.New("plugin catalog sourceUrl origin does not match the configured source")
	}
	if err := atomicWrite(destination, append(data, '\n')); err != nil {
		return Catalog{}, err
	}
	return catalog, nil
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
		haystack := strings.ToLower(item.ID + " " + item.Name + " " + item.Developer + " " + item.Description + " " + strings.Join(item.Tags, " "))
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
	if catalog.SchemaVersion != 1 || !idPattern.MatchString(catalog.ID) || strings.TrimSpace(catalog.Name) == "" || strings.TrimSpace(catalog.License) == "" {
		return Catalog{}, errors.New("plugin catalog identity is invalid")
	}
	if err := validateHTTPSURL(catalog.SourceURL, "catalog sourceUrl"); err != nil {
		return Catalog{}, err
	}
	if _, err := time.Parse(time.RFC3339, catalog.GeneratedUTC); err != nil {
		return Catalog{}, errors.New("plugin catalog generatedUtc is invalid")
	}
	if len(catalog.Plugins) > 5000 {
		return Catalog{}, errors.New("plugin catalog exceeds 5000 entries")
	}
	seen := map[string]bool{}
	for _, item := range catalog.Plugins {
		if err := validateCatalogItem(item); err != nil {
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
	if item.Rating < 0 || item.Rating > 5 || item.Downloads < 0 {
		return errors.New("rating/download count is invalid")
	}
	if _, err := time.Parse(time.RFC3339, item.UpdatedUTC); err != nil {
		return errors.New("updatedUtc is invalid")
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
