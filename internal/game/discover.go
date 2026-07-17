package game

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

// DiscoverRoots inspects ordered roots and returns all unique valid candidates.
// Invalid roots are warnings; cancellation is returned immediately.
func DiscoverRoots(ctx context.Context, roots []string, customExecutable string) (Discovery, error) {
	var result Discovery
	seen := make(map[string]struct{})
	for _, root := range roots {
		select {
		case <-ctx.Done():
			return Discovery{}, ctx.Err()
		default:
		}
		if strings.TrimSpace(root) == "" {
			continue
		}
		candidate, err := InspectRoot(root, customExecutable)
		if err != nil {
			var ambiguous *AmbiguousError
			if errors.As(err, &ambiguous) {
				result.Warnings = append(result.Warnings, err.Error())
			}
			continue
		}
		key := strings.ToLower(filepath.Clean(candidate.Executable))
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result.Candidates = append(result.Candidates, candidate)
	}
	return result, nil
}

func SelectSingle(discovery Discovery) (Candidate, error) {
	switch len(discovery.Candidates) {
	case 0:
		return Candidate{}, errors.New("no game installation found")
	case 1:
		return discovery.Candidates[0], nil
	default:
		return Candidate{}, fmt.Errorf("found %d game installations; explicit selection is required", len(discovery.Candidates))
	}
}
