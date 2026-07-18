package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"genshintools/internal/selfupdate"
)

type auditOptions struct {
	PackagePath   string
	ManifestPath  string
	PublicKeyPath string
}

type buildInfo struct {
	Version string `json:"version"`
}

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(arguments []string, stdout, stderr io.Writer) int {
	options, err := parseOptions(arguments, stderr)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	if err := audit(options); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	fmt.Fprintln(stdout, "release package audit passed")
	return 0
}

func parseOptions(arguments []string, stderr io.Writer) (auditOptions, error) {
	flags := flag.NewFlagSet("release-audit", flag.ContinueOnError)
	flags.SetOutput(stderr)
	var options auditOptions
	flags.StringVar(&options.PackagePath, "package", "", "release ZIP path")
	flags.StringVar(&options.ManifestPath, "manifest", "", "signed manifest path")
	flags.StringVar(&options.PublicKeyPath, "public-key", "", "Ed25519 public key file")
	if err := flags.Parse(arguments); err != nil {
		return auditOptions{}, err
	}
	if flags.NArg() != 0 {
		return auditOptions{}, errors.New("unexpected positional arguments")
	}
	for name, value := range map[string]string{"package": options.PackagePath, "manifest": options.ManifestPath, "public-key": options.PublicKeyPath} {
		if strings.TrimSpace(value) == "" {
			return auditOptions{}, fmt.Errorf("--%s is required", name)
		}
	}
	return options, nil
}

func audit(options auditOptions) error {
	packageInfo, err := os.Stat(options.PackagePath)
	if err != nil || !packageInfo.Mode().IsRegular() || packageInfo.Size() <= 0 {
		return errors.New("release package must be an existing regular file")
	}
	packageSize, packageHash, err := hashFile(options.PackagePath)
	if err != nil {
		return fmt.Errorf("hash release package: %w", err)
	}
	manifest, err := loadManifest(options.ManifestPath)
	if err != nil {
		return err
	}
	published, err := time.Parse(time.RFC3339, manifest.PublishedUTC)
	if err != nil || published.After(time.Now().UTC().Add(5*time.Minute)) {
		return errors.New("release manifest publication time is invalid or in the future")
	}
	publicKey, err := readPublicKey(options.PublicKeyPath)
	if err != nil {
		return err
	}
	payload, err := selfupdate.CanonicalPayload(manifest)
	if err != nil {
		return fmt.Errorf("validate manifest metadata: %w", err)
	}
	signature, err := base64.StdEncoding.DecodeString(manifest.Signature)
	if err != nil || !ed25519.Verify(publicKey, payload, signature) {
		return errors.New("release manifest signature verification failed")
	}
	var artifact *selfupdate.Artifact
	for index := range manifest.Artifacts {
		candidate := &manifest.Artifacts[index]
		if candidate.OS == "windows" && candidate.Arch == "amd64" {
			artifact = candidate
			break
		}
	}
	if artifact == nil || artifact.Size != packageSize || !strings.EqualFold(artifact.SHA256, packageHash) {
		return errors.New("manifest artifact size or SHA-256 does not match the release ZIP")
	}
	stagingRoot, err := os.MkdirTemp("", "genshintools-release-audit-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stagingRoot)
	staged, err := selfupdate.StagePackage(context.Background(), options.PackagePath, stagingRoot, manifest.Version, *artifact)
	if err != nil {
		return fmt.Errorf("audit release ZIP: %w", err)
	}
	data, err := os.ReadFile(filepath.Join(staged.Directory, "build-info.json"))
	if err != nil {
		return err
	}
	var info buildInfo
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(&info); err != nil || info.Version != manifest.Version {
		return errors.New("build-info.json version does not match the signed manifest")
	}
	return nil
}

func loadManifest(path string) (selfupdate.Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return selfupdate.Manifest{}, fmt.Errorf("read manifest: %w", err)
	}
	var manifest selfupdate.Manifest
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return selfupdate.Manifest{}, fmt.Errorf("decode manifest: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return selfupdate.Manifest{}, errors.New("manifest contains trailing JSON")
	}
	return manifest, nil
}

func readPublicKey(path string) (ed25519.PublicKey, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read public key: %w", err)
	}
	raw := data
	trimmed := bytes.TrimSpace(data)
	if len(raw) != ed25519.PublicKeySize {
		raw, err = decodeKeyText(trimmed)
		if err != nil {
			return nil, err
		}
	}
	if len(raw) != ed25519.PublicKeySize {
		return nil, errors.New("public key must be a 32-byte raw, base64, or hex Ed25519 key")
	}
	return ed25519.PublicKey(append([]byte(nil), raw...)), nil
}

func decodeKeyText(data []byte) ([]byte, error) {
	if decoded, err := base64.StdEncoding.DecodeString(string(data)); err == nil {
		return decoded, nil
	}
	if decoded, err := hex.DecodeString(string(data)); err == nil {
		return decoded, nil
	}
	return nil, errors.New("key must be raw, base64, or hex")
}

func hashFile(path string) (int64, string, error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, "", err
	}
	defer file.Close()
	hash := sha256.New()
	size, err := io.Copy(hash, file)
	if err != nil {
		return 0, "", err
	}
	return size, hex.EncodeToString(hash.Sum(nil)), nil
}
