package selfupdate

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

func FetchManifest(ctx context.Context, client *http.Client, manifestURL string) ([]byte, error) {
	parsed, err := url.Parse(manifestURL)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" {
		return nil, errors.New("update manifest URL must be an absolute HTTPS URL without credentials or fragment")
	}
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, manifestURL, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "GenshinTools-SelfUpdate/1")
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("update manifest HTTP status %d", response.StatusCode)
	}
	if response.Request == nil || response.Request.URL == nil || !sameOrigin(manifestURL, response.Request.URL.String()) {
		return nil, errors.New("update manifest redirect changed the configured HTTPS origin")
	}
	if response.ContentLength > maxManifestBytes {
		return nil, errors.New("update manifest exceeds 1 MiB")
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxManifestBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxManifestBytes {
		return nil, errors.New("update manifest exceeds 1 MiB")
	}
	return data, nil
}
