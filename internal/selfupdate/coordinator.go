package selfupdate

import (
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

type Coordinator struct {
	InstallRoot    string
	CurrentVersion string
	ManifestURL    string
	HTTPClient     *http.Client
	TrustedKeys    map[string]ed25519.PublicKey
}

func (c Coordinator) layout() (UpdateLayout, error) {
	if c.InstallRoot == "" || c.CurrentVersion == "" {
		return UpdateLayout{}, errors.New("update coordinator requires install root and current version")
	}
	layout, err := NewUpdateLayout(c.InstallRoot)
	if err != nil {
		return UpdateLayout{}, err
	}
	if err := layout.Ensure(); err != nil {
		return UpdateLayout{}, err
	}
	return layout, nil
}

func (c Coordinator) Check(ctx context.Context) (Release, error) {
	manifestURL := c.ManifestURL
	if manifestURL == "" {
		var err error
		manifestURL, err = BuiltInManifestURL()
		if err != nil {
			return Release{}, err
		}
	}
	var err error
	keys := c.TrustedKeys
	if keys == nil {
		keys, err = BuiltInKeys()
		if err != nil {
			return Release{}, err
		}
	}
	data, err := FetchManifest(ctx, c.HTTPClient, manifestURL)
	if err != nil {
		return Release{}, err
	}
	return DecodeAndVerify(data, keys, c.CurrentVersion, runtime.GOOS, runtime.GOARCH, time.Now().UTC())
}

func (c Coordinator) DownloadAndStage(ctx context.Context, release Release) (StagedRelease, error) {
	layout, err := c.layout()
	if err != nil {
		return StagedRelease{}, err
	}
	if CompareVersions(release.Manifest.Version, c.CurrentVersion) <= 0 {
		return StagedRelease{}, errors.New("update release is not newer than current version")
	}
	packagePath := filepath.Join(layout.Downloads, "GenshinTools-"+release.Manifest.Version+".zip")
	if err := DownloadArtifact(ctx, c.HTTPClient, release.Artifact, packagePath); err != nil {
		return StagedRelease{}, fmt.Errorf("download update package: %w", err)
	}
	staged, err := StagePackage(ctx, packagePath, layout.Versions, release.Manifest.Version, release.Artifact)
	if err != nil {
		return StagedRelease{}, fmt.Errorf("stage update package: %w", err)
	}
	return staged, nil
}

func (c Coordinator) PrepareAndLaunch(ctx context.Context, staged StagedRelease) (UpdaterLaunch, error) {
	if err := ctx.Err(); err != nil {
		return UpdaterLaunch{}, err
	}
	layout, err := c.layout()
	if err != nil {
		return UpdaterLaunch{}, err
	}
	identity, err := CurrentProcessIdentity()
	if err != nil {
		return UpdaterLaunch{}, fmt.Errorf("capture launcher process identity: %w", err)
	}
	request := UpdaterRequest{
		ProtocolVersion:       UpdaterProtocolVersion,
		Version:               staged.Manifest.Version,
		ManifestSHA256:        staged.ManifestSHA256,
		Parent:                identity,
		WaitTimeoutMS:         120_000,
		ConfirmationTimeoutMS: 30_000,
		Restart:               true,
	}
	launch, err := PrepareUpdater(layout, request)
	if err != nil {
		return UpdaterLaunch{}, err
	}
	if err := StartUpdater(launch); err != nil {
		_ = os.Remove(launch.RequestPath)
		return UpdaterLaunch{}, fmt.Errorf("start update helper: %w", err)
	}
	return launch, nil
}
