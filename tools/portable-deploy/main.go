// Command portable-deploy installs a verified portable ZIP while preserving
// only the target's runtime data directory. All other target content is
// replaced transactionally so files retired by a release cannot accumulate.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"genshintools/internal/selfupdate"
)

var movePath = os.Rename

func main() {
	var archive, target, version string
	flag.StringVar(&archive, "archive", "", "portable ZIP path")
	flag.StringVar(&target, "target", "", "deployment directory")
	flag.StringVar(&version, "version", "", "expected product SemVer")
	flag.Parse()
	if err := deploy(context.Background(), archive, target, version); err != nil {
		fmt.Fprintln(os.Stderr, "portable deployment failed:", err)
		os.Exit(1)
	}
	fmt.Printf("Deployed Genshin Tools %s to %s\n", version, target)
}

func deploy(ctx context.Context, archive, target, version string) error {
	archive, err := filepath.Abs(archive)
	if err != nil {
		return err
	}
	target, err = filepath.Abs(target)
	if err != nil {
		return err
	}
	if filepath.Dir(target) == target {
		return errors.New("deployment target must not be a volume root")
	}
	if info, statErr := os.Lstat(target); statErr == nil {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("deployment target must be a real directory")
		}
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return statErr
	}
	size, digest, err := hashFile(archive)
	if err != nil {
		return err
	}
	parent := filepath.Dir(target)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return err
	}
	stagingRoot, err := os.MkdirTemp(parent, ".genshintools-deploy-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stagingRoot)
	artifact := selfupdate.Artifact{
		OS: "windows", Arch: "amd64", URL: "https://deploy.invalid/" + filepath.Base(archive),
		Size: size, SHA256: digest,
	}
	staged, err := selfupdate.StagePackage(ctx, archive, stagingRoot, version, artifact)
	if err != nil {
		return fmt.Errorf("verify portable ZIP: %w", err)
	}
	if err := mirrorRelease(staged.Directory, target); err != nil {
		return err
	}
	return nil
}

func mirrorRelease(source, target string) error {
	incoming, err := os.ReadDir(source)
	if err != nil {
		return err
	}
	for _, entry := range incoming {
		if strings.EqualFold(entry.Name(), "data") {
			return errors.New("portable release must not contain a data directory")
		}
	}
	if err := os.MkdirAll(target, 0o755); err != nil {
		return err
	}
	existing, err := os.ReadDir(target)
	if err != nil {
		return err
	}
	backup, err := os.MkdirTemp(filepath.Dir(target), ".genshintools-backup-*")
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if committed {
			_ = os.RemoveAll(backup)
		}
	}()

	var backedUp, installed []string
	rollback := func(cause error) error {
		var rollbackErrors []error
		for i := len(installed) - 1; i >= 0; i-- {
			name := installed[i]
			if moveErr := movePath(filepath.Join(target, name), filepath.Join(source, name)); moveErr != nil {
				rollbackErrors = append(rollbackErrors, fmt.Errorf("remove new %s: %w", name, moveErr))
			}
		}
		for i := len(backedUp) - 1; i >= 0; i-- {
			name := backedUp[i]
			if moveErr := movePath(filepath.Join(backup, name), filepath.Join(target, name)); moveErr != nil {
				rollbackErrors = append(rollbackErrors, fmt.Errorf("restore old %s: %w", name, moveErr))
			}
		}
		_ = os.RemoveAll(backup)
		return errors.Join(append([]error{cause}, rollbackErrors...)...)
	}
	for _, entry := range existing {
		if strings.EqualFold(entry.Name(), "data") {
			info, infoErr := entry.Info()
			if infoErr != nil {
				return rollback(fmt.Errorf("inspect data directory: %w", infoErr))
			}
			if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
				return rollback(errors.New("preserved data path must be a real directory"))
			}
			continue
		}
		if err := movePath(filepath.Join(target, entry.Name()), filepath.Join(backup, entry.Name())); err != nil {
			return rollback(fmt.Errorf("stage old %s: %w", entry.Name(), err))
		}
		backedUp = append(backedUp, entry.Name())
	}
	for _, entry := range incoming {
		if err := movePath(filepath.Join(source, entry.Name()), filepath.Join(target, entry.Name())); err != nil {
			return rollback(fmt.Errorf("install new %s: %w", entry.Name(), err))
		}
		installed = append(installed, entry.Name())
	}
	committed = true
	return nil
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
