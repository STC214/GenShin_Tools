package main

import (
	"bytes"
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

type manifestOptions struct {
	PackagePath  string
	OutputPath   string
	Version      string
	Minimum      string
	URL          string
	KeyID        string
	PrivateKey   string
	PublishedUTC string
	Channel      string
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(arguments []string, stdout, stderr io.Writer) int {
	options, err := parseOptions(arguments, stderr)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	if err := generateManifest(options); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	fmt.Fprintf(stdout, "wrote signed manifest %s\n", options.OutputPath)
	return 0
}

func parseOptions(arguments []string, stderr io.Writer) (manifestOptions, error) {
	flags := flag.NewFlagSet("release-manifest", flag.ContinueOnError)
	flags.SetOutput(stderr)
	var options manifestOptions
	flags.StringVar(&options.PackagePath, "package", "", "release ZIP path")
	flags.StringVar(&options.OutputPath, "output", "", "manifest output path")
	flags.StringVar(&options.Version, "version", "", "release SemVer")
	flags.StringVar(&options.Minimum, "minimum-version", "", "minimum supported SemVer")
	flags.StringVar(&options.URL, "url", "", "HTTPS artifact URL")
	flags.StringVar(&options.KeyID, "key-id", "", "trusted signing key ID")
	flags.StringVar(&options.PrivateKey, "private-key", "", "private key file (raw, base64, or hex)")
	flags.StringVar(&options.PublishedUTC, "published-utc", "", "RFC3339 publication time (default: now)")
	flags.StringVar(&options.Channel, "channel", "stable", "release channel")
	if err := flags.Parse(arguments); err != nil {
		return manifestOptions{}, err
	}
	if flags.NArg() != 0 {
		return manifestOptions{}, errors.New("unexpected positional arguments")
	}
	required := []struct{ name, value string }{
		{"package", options.PackagePath}, {"output", options.OutputPath}, {"version", options.Version},
		{"minimum-version", options.Minimum}, {"url", options.URL}, {"key-id", options.KeyID}, {"private-key", options.PrivateKey},
	}
	for _, option := range required {
		if strings.TrimSpace(option.value) == "" {
			return manifestOptions{}, fmt.Errorf("--%s is required", option.name)
		}
	}
	if options.PublishedUTC == "" {
		options.PublishedUTC = time.Now().UTC().Format(time.RFC3339)
	}
	return options, nil
}

func generateManifest(options manifestOptions) error {
	info, err := os.Stat(options.PackagePath)
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 {
		return errors.New("release package must be an existing non-empty regular file")
	}
	size, digest, err := hashFile(options.PackagePath)
	if err != nil {
		return fmt.Errorf("hash release package: %w", err)
	}
	manifest := selfupdate.Manifest{
		SchemaVersion:  1,
		Channel:        options.Channel,
		Version:        options.Version,
		PublishedUTC:   options.PublishedUTC,
		MinimumVersion: options.Minimum,
		Artifacts:      []selfupdate.Artifact{{OS: "windows", Arch: "amd64", URL: options.URL, Size: size, SHA256: digest}},
		KeyID:          options.KeyID,
	}
	privateKey, err := readPrivateKey(options.PrivateKey)
	if err != nil {
		return err
	}
	payload, err := selfupdate.CanonicalPayload(manifest)
	if err != nil {
		return fmt.Errorf("validate release metadata: %w", err)
	}
	manifest.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, payload))
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	publicKey := privateKey.Public().(ed25519.PublicKey)
	verifyPayload, err := selfupdate.CanonicalPayload(manifest)
	if err != nil {
		return fmt.Errorf("self-verify generated manifest payload: %w", err)
	}
	signature, err := base64.StdEncoding.DecodeString(manifest.Signature)
	if err != nil || !ed25519.Verify(publicKey, verifyPayload, signature) {
		if err == nil {
			err = errors.New("signature verification failed")
		}
		return fmt.Errorf("self-verify generated manifest: %w", err)
	}
	return writeNewFile(options.OutputPath, append(data, '\n'))
}

func readPrivateKey(path string) (ed25519.PrivateKey, error) {
	data, err := readSmallFile(path, 1<<10)
	if err != nil {
		return nil, fmt.Errorf("read signing key: %w", err)
	}
	rawData := data
	data = bytes.TrimSpace(data)
	decode := func(value []byte) ([]byte, bool) {
		if decoded, err := base64.StdEncoding.DecodeString(string(value)); err == nil {
			return decoded, true
		}
		if decoded, err := hex.DecodeString(string(value)); err == nil {
			return decoded, true
		}
		return nil, false
	}
	keyBytes := rawData
	if len(rawData) != ed25519.PrivateKeySize && len(rawData) != ed25519.SeedSize {
		decoded, ok := decode(data)
		if !ok {
			return nil, errors.New("signing key must be raw, base64, or hex Ed25519 key")
		}
		keyBytes = decoded
	}
	if len(keyBytes) == ed25519.SeedSize {
		return ed25519.NewKeyFromSeed(keyBytes), nil
	}
	if len(keyBytes) != ed25519.PrivateKeySize {
		return nil, errors.New("signing key must be 32-byte seed or 64-byte private key")
	}
	return ed25519.PrivateKey(append([]byte(nil), keyBytes...)), nil
}

func readSmallFile(path string, maximum int64) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maximum+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maximum {
		return nil, fmt.Errorf("%s exceeds %d bytes", filepath.Base(path), maximum)
	}
	return data, nil
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

func writeNewFile(path string, data []byte) error {
	if path, _ = filepath.Abs(path); path == "" {
		return errors.New("manifest output path is invalid")
	}
	if _, err := os.Lstat(path); err == nil {
		return errors.New("refusing to overwrite an existing manifest")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err = file.Write(data); err == nil {
		err = file.Sync()
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(path)
	}
	return err
}
