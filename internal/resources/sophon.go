package resources

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/klauspost/compress/zstd"
)

const (
	DefaultSophonBuildURL    = "https://api-takumi.mihoyo.com/downloader/sophon_chunk/api/getBuild?branch=main&package_id=8xfMve0uwQ&password=CW8GbLNU8f&plat_app=ddxf5qt290cg"
	DefaultSophonBranchesURL = "https://hyp-api.mihoyo.com/hyp/hyp-connect/api/getGameBranches?launcher_id=jGHBHlcOq1&language=zh-cn&game_ids[]=1Z8W5NHUQb"
	sophonBuildEndpoint      = "https://api-takumi.mihoyo.com/downloader/sophon_chunk/api/getBuild"
	sophonPlatformApp        = "ddxf5qt290cg"
	maxBuildResponseBytes    = 4 << 20
	maxCompressedManifest    = 64 << 20
	maxSophonManifest        = 256 << 20
)

type SophonProvider struct {
	Client      *http.Client
	BuildURL    string
	BranchesURL string
	MaxAttempts int
	RetryDelay  time.Duration
}

type SophonBranches struct {
	Current     SophonBranch
	PreDownload *SophonBranch
}

type SophonBranch struct {
	PackageID string
	Branch    string
	Password  string
	Tag       string
}

type sophonBranchesResponse struct {
	Retcode int                `json:"retcode"`
	Message string             `json:"message"`
	Data    sophonBranchesData `json:"data"`
}

type sophonBranchesData struct {
	Branches []sophonBranchEntry `json:"game_branches"`
}

type sophonBranchEntry struct {
	Game                     sophonGame        `json:"game"`
	Main                     sophonBranchData  `json:"main"`
	PreDownload              *sophonBranchData `json:"pre_download"`
	EnableBasePkgPreDownload bool              `json:"enable_base_pkg_predownload"`
}

type sophonGame struct {
	ID  string `json:"id"`
	Biz string `json:"biz"`
}

type sophonBranchData struct {
	PackageID             string           `json:"package_id"`
	Branch                string           `json:"branch"`
	Password              string           `json:"password"`
	Tag                   string           `json:"tag"`
	DiffTags              []string         `json:"diff_tags"`
	Categories            []sophonCategory `json:"categories"`
	RequiredClientVersion string           `json:"required_client_version"`
}

type sophonCategory struct {
	CategoryID    string   `json:"category_id"`
	MatchingField string   `json:"matching_field"`
	Type          string   `json:"type"`
	Scenarios     []string `json:"scenarios"`
}

type SophonCatalog struct {
	Version string
	assets  map[string]sophonAsset
}

type SophonAssetInfo struct {
	Name              string
	CompressedBytes   int64
	UncompressedBytes int64
}

type sophonBuildResponse struct {
	Retcode int             `json:"retcode"`
	Message string          `json:"message"`
	Data    sophonBuildData `json:"data"`
}

type sophonBuildData struct {
	BuildID   string        `json:"build_id"`
	Tag       string        `json:"tag"`
	Manifests []sophonAsset `json:"manifests"`
}

type sophonAsset struct {
	CategoryID       string          `json:"category_id"`
	CategoryName     string          `json:"category_name"`
	Manifest         sophonObject    `json:"manifest"`
	ChunkDownload    sophonDownload  `json:"chunk_download"`
	ManifestDownload sophonDownload  `json:"manifest_download"`
	MatchingField    string          `json:"matching_field"`
	Stats            json.RawMessage `json:"stats"`
	Deduplicated     json.RawMessage `json:"deduplicated_stats"`
}

type sophonObject struct {
	ID               string     `json:"id"`
	Checksum         string     `json:"checksum"`
	CompressedSize   sophonSize `json:"compressed_size"`
	UncompressedSize sophonSize `json:"uncompressed_size"`
}

type sophonSize int64

func (s *sophonSize) UnmarshalJSON(data []byte) error {
	var value string
	if err := json.Unmarshal(data, &value); err != nil {
		return errors.New("Sophon size must be a decimal JSON string")
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed < 0 {
		return fmt.Errorf("invalid Sophon size %q", value)
	}
	*s = sophonSize(parsed)
	return nil
}

type sophonDownload struct {
	Encryption  int    `json:"encryption"`
	Password    string `json:"password"`
	Compression int    `json:"compression"`
	URLPrefix   string `json:"url_prefix"`
	URLSuffix   string `json:"url_suffix"`
}

func NewSophonProvider() *SophonProvider {
	return &SophonProvider{
		Client:      &http.Client{Timeout: 45 * time.Second},
		BuildURL:    DefaultSophonBuildURL,
		BranchesURL: DefaultSophonBranchesURL,
		MaxAttempts: 3,
		RetryDelay:  time.Second,
	}
}

func (p *SophonProvider) FetchBranches(ctx context.Context) (SophonBranches, error) {
	client := p.Client
	if client == nil {
		client = NewSophonProvider().Client
	}
	branchesURL := p.BranchesURL
	if branchesURL == "" {
		branchesURL = DefaultSophonBranchesURL
	}
	if err := validateHTTPURL(branchesURL); err != nil {
		return SophonBranches{}, err
	}
	attempts := p.MaxAttempts
	if attempts == 0 {
		attempts = 3
	}
	data, err := p.getBounded(ctx, client, branchesURL, maxBuildResponseBytes, attempts)
	if err != nil {
		return SophonBranches{}, fmt.Errorf("fetch Sophon branches: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var response sophonBranchesResponse
	if err := decoder.Decode(&response); err != nil {
		return SophonBranches{}, fmt.Errorf("decode Sophon branches schema: %w", err)
	}
	if response.Retcode != 0 || response.Message != "OK" {
		return SophonBranches{}, fmt.Errorf("Sophon branches API rejected request: retcode=%d message=%q", response.Retcode, response.Message)
	}
	for _, entry := range response.Data.Branches {
		if entry.Game.ID != "1Z8W5NHUQb" || entry.Game.Biz != "hk4e_cn" {
			continue
		}
		current, err := convertSophonBranch(entry.Main)
		if err != nil {
			return SophonBranches{}, fmt.Errorf("invalid current branch: %w", err)
		}
		result := SophonBranches{Current: current}
		if entry.PreDownload != nil {
			preDownload, err := convertSophonBranch(*entry.PreDownload)
			if err != nil {
				return SophonBranches{}, fmt.Errorf("invalid pre-download branch: %w", err)
			}
			result.PreDownload = &preDownload
		}
		return result, nil
	}
	return SophonBranches{}, errors.New("Sophon branches API contains no audited mainland Genshin entry")
}

func convertSophonBranch(data sophonBranchData) (SophonBranch, error) {
	if data.PackageID == "" || data.Branch == "" || data.Password == "" || data.Tag == "" || len(data.Categories) == 0 {
		return SophonBranch{}, errors.New("branch identity, credentials, tag and categories are required")
	}
	return SophonBranch{PackageID: data.PackageID, Branch: data.Branch, Password: data.Password, Tag: data.Tag}, nil
}

func (b SophonBranch) BuildURL() (string, error) {
	if b.PackageID == "" || b.Branch == "" || b.Password == "" || b.Tag == "" {
		return "", errors.New("incomplete Sophon branch")
	}
	values := url.Values{}
	values.Set("branch", b.Branch)
	values.Set("package_id", b.PackageID)
	values.Set("password", b.Password)
	values.Set("plat_app", sophonPlatformApp)
	values.Set("tag", b.Tag)
	return sophonBuildEndpoint + "?" + values.Encode(), nil
}

func (p *SophonProvider) FetchCatalog(ctx context.Context) (SophonCatalog, error) {
	client, buildURL, attempts, err := p.normalized()
	if err != nil {
		return SophonCatalog{}, err
	}
	data, err := p.getBounded(ctx, client, buildURL, maxBuildResponseBytes, attempts)
	if err != nil {
		return SophonCatalog{}, fmt.Errorf("fetch Sophon build: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var response sophonBuildResponse
	if err := decoder.Decode(&response); err != nil {
		return SophonCatalog{}, fmt.Errorf("decode Sophon build schema: %w", err)
	}
	if err := ensureJSONEnd(decoder); err != nil {
		return SophonCatalog{}, err
	}
	if response.Retcode != 0 || response.Message != "OK" {
		return SophonCatalog{}, fmt.Errorf("Sophon API rejected request: retcode=%d message=%q", response.Retcode, response.Message)
	}
	if response.Data.BuildID == "" || response.Data.Tag == "" || len(response.Data.Manifests) == 0 {
		return SophonCatalog{}, errors.New("Sophon API returned incomplete or empty build data")
	}
	catalog := SophonCatalog{Version: response.Data.Tag, assets: make(map[string]sophonAsset, len(response.Data.Manifests))}
	for _, asset := range response.Data.Manifests {
		name := strings.ToLower(strings.TrimSpace(asset.MatchingField))
		if name == "" {
			return SophonCatalog{}, errors.New("Sophon asset has no matching_field")
		}
		if _, exists := catalog.assets[name]; exists {
			return SophonCatalog{}, fmt.Errorf("duplicate Sophon asset %q", name)
		}
		if err := validateSophonAsset(asset); err != nil {
			return SophonCatalog{}, fmt.Errorf("Sophon asset %q: %w", name, err)
		}
		catalog.assets[name] = asset
	}
	if _, exists := catalog.assets["game"]; !exists {
		return SophonCatalog{}, errors.New("Sophon catalog contains no base game asset")
	}
	return catalog, nil
}

func (c SophonCatalog) Assets() []SophonAssetInfo {
	result := make([]SophonAssetInfo, 0, len(c.assets))
	for name, asset := range c.assets {
		result = append(result, SophonAssetInfo{Name: name, CompressedBytes: int64(asset.Manifest.CompressedSize), UncompressedBytes: int64(asset.Manifest.UncompressedSize)})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result
}

func (p *SophonProvider) LoadManifest(ctx context.Context, catalog SophonCatalog, names ...string) (Manifest, error) {
	client, _, attempts, err := p.normalized()
	if err != nil {
		return Manifest{}, err
	}
	if catalog.Version == "" || len(catalog.assets) == 0 || len(names) == 0 {
		return Manifest{}, errors.New("catalog and at least one asset selection are required")
	}
	result := Manifest{SchemaVersion: ManifestSchemaVersion, Version: catalog.Version, Kind: "sophon"}
	selected := make(map[string]struct{}, len(names))
	for _, requested := range names {
		name := strings.ToLower(strings.TrimSpace(requested))
		if _, duplicate := selected[name]; duplicate {
			continue
		}
		selected[name] = struct{}{}
		asset, exists := catalog.assets[name]
		if !exists {
			return Manifest{}, fmt.Errorf("Sophon asset %q is not available", requested)
		}
		manifestURL := joinSophonURL(asset.ManifestDownload, asset.Manifest.ID)
		compressed, err := p.getBounded(ctx, client, manifestURL, int64(asset.Manifest.CompressedSize), attempts)
		if err != nil {
			return Manifest{}, fmt.Errorf("download Sophon manifest %q: %w", name, err)
		}
		decoded, err := decompressSophonManifest(compressed, asset.Manifest)
		if err != nil {
			return Manifest{}, fmt.Errorf("verify Sophon manifest %q: %w", name, err)
		}
		files, err := parseSophonManifest(decoded)
		if err != nil {
			return Manifest{}, fmt.Errorf("parse Sophon manifest %q: %w", name, err)
		}
		for _, file := range files {
			if file.Folder {
				continue
			}
			converted := ManifestFile{Path: file.Path, Size: file.Size, Hash: Hash{Algorithm: "md5", Digest: file.Checksum}}
			for _, chunk := range file.Chunks {
				converted.Chunks = append(converted.Chunks, ManifestChunk{
					ID: chunk.ID, URL: joinSophonURL(asset.ChunkDownload, chunk.ID), Offset: chunk.Offset,
					CompressedSize: chunk.CompressedSize, Size: chunk.Size, Hash: Hash{Algorithm: "md5", Digest: chunk.Checksum},
				})
			}
			result.Files = append(result.Files, converted)
		}
	}
	if err := result.Validate(); err != nil {
		return Manifest{}, fmt.Errorf("converted Sophon manifest: %w", err)
	}
	return result, nil
}

func validateSophonAsset(asset sophonAsset) error {
	object := asset.Manifest
	if object.ID == "" || object.CompressedSize <= 0 || object.CompressedSize > maxCompressedManifest || object.UncompressedSize <= 0 || object.UncompressedSize > maxSophonManifest {
		return errors.New("manifest identity or declared size is invalid")
	}
	if err := (Hash{Algorithm: "md5", Digest: object.Checksum}).Validate(); err != nil {
		return err
	}
	for label, download := range map[string]sophonDownload{"manifest": asset.ManifestDownload, "chunk": asset.ChunkDownload} {
		if download.Encryption != 0 || download.Compression != 1 || download.Password != "" {
			return fmt.Errorf("unsupported %s encryption/compression settings", label)
		}
		if err := validateHTTPURL(download.URLPrefix); err != nil {
			return fmt.Errorf("invalid %s URL prefix", label)
		}
	}
	return nil
}

func decompressSophonManifest(compressed []byte, object sophonObject) ([]byte, error) {
	if int64(len(compressed)) != int64(object.CompressedSize) {
		return nil, fmt.Errorf("compressed size %d, want %d", len(compressed), object.CompressedSize)
	}
	decoder, err := zstd.NewReader(bytes.NewReader(compressed), zstd.WithDecoderMaxMemory(maxSophonManifest))
	if err != nil {
		return nil, err
	}
	defer decoder.Close()
	decoded, err := io.ReadAll(io.LimitReader(decoder, int64(object.UncompressedSize)+1))
	if err != nil {
		return nil, err
	}
	if int64(len(decoded)) != int64(object.UncompressedSize) {
		return nil, fmt.Errorf("uncompressed size %d, want %d", len(decoded), object.UncompressedSize)
	}
	digest := md5.Sum(decoded)
	if !strings.EqualFold(hex.EncodeToString(digest[:]), object.Checksum) {
		return nil, errors.New("manifest MD5 mismatch")
	}
	return decoded, nil
}

func joinSophonURL(download sophonDownload, id string) string {
	return strings.TrimRight(download.URLPrefix, "/") + "/" + url.PathEscape(id) + download.URLSuffix
}

func (p *SophonProvider) normalized() (*http.Client, string, int, error) {
	client := p.Client
	if client == nil {
		client = NewSophonProvider().Client
	}
	buildURL := p.BuildURL
	if buildURL == "" {
		buildURL = DefaultSophonBuildURL
	}
	if err := validateHTTPURL(buildURL); err != nil {
		return nil, "", 0, err
	}
	attempts := p.MaxAttempts
	if attempts == 0 {
		attempts = 3
	}
	if attempts < 1 || attempts > 5 {
		return nil, "", 0, errors.New("Sophon attempts must be within 1..5")
	}
	return client, buildURL, attempts, nil
}

func (p *SophonProvider) getBounded(ctx context.Context, client *http.Client, rawURL string, maximum int64, attempts int) ([]byte, error) {
	if maximum <= 0 {
		return nil, errors.New("download size limit must be positive")
	}
	var last error
	for attempt := 1; attempt <= attempts; attempt++ {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
		if err != nil {
			return nil, err
		}
		request.Header.Set("User-Agent", "GenshinTools/0.4 (Windows; resource-audit)")
		response, err := client.Do(request)
		if err == nil {
			if response.StatusCode == http.StatusOK {
				data, readErr := io.ReadAll(io.LimitReader(response.Body, maximum+1))
				closeErr := response.Body.Close()
				if readErr == nil && closeErr == nil && int64(len(data)) <= maximum {
					return data, nil
				}
				last = errors.Join(readErr, closeErr)
				if int64(len(data)) > maximum {
					last = fmt.Errorf("response exceeds %d bytes", maximum)
				}
			} else {
				_ = response.Body.Close()
				last = fmt.Errorf("server returned %s", response.Status)
				if response.StatusCode < 500 && response.StatusCode != http.StatusTooManyRequests {
					return nil, last
				}
			}
		} else {
			last = err
		}
		if attempt < attempts {
			delay := p.RetryDelay
			if delay <= 0 {
				delay = time.Second
			}
			select {
			case <-time.After(delay * time.Duration(attempt)):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
	}
	return nil, last
}
