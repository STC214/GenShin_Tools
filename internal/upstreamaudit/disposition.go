package upstreamaudit

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

const maxDispositionBytes = 1 << 20

type Disposition struct {
	SchemaVersion int               `json:"schemaVersion"`
	Base          string            `json:"base"`
	Head          string            `json:"head"`
	Reviewer      string            `json:"reviewer"`
	ReviewedUTC   string            `json:"reviewedUtc"`
	Items         []DispositionItem `json:"items"`
}

type DispositionItem struct {
	Path                     string         `json:"path"`
	Classification           Classification `json:"classification"`
	Decision                 string         `json:"decision"`
	References               []string       `json:"references"`
	BinaryReviewed           bool           `json:"binaryReviewed"`
	SourceAndLicenseReviewed bool           `json:"sourceAndLicenseReviewed"`
	APISchemaReviewed        bool           `json:"apiSchemaReviewed"`
	ExclusionsReviewed       bool           `json:"exclusionsReviewed"`
}

func LoadDisposition(reader io.Reader) (Disposition, error) {
	data, err := io.ReadAll(io.LimitReader(reader, maxDispositionBytes+1))
	if err != nil {
		return Disposition{}, err
	}
	if len(data) > maxDispositionBytes {
		return Disposition{}, errors.New("upstream disposition exceeds 1 MiB")
	}
	var disposition Disposition
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&disposition); err != nil {
		return Disposition{}, fmt.Errorf("decode upstream disposition: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return Disposition{}, errors.New("upstream disposition contains trailing JSON")
	}
	return disposition, nil
}

func ValidateDisposition(report Report, disposition Disposition) error {
	if disposition.SchemaVersion != 1 || disposition.Base != report.Base || disposition.Head != report.Head {
		return errors.New("disposition schema or base/head does not match the report")
	}
	if strings.TrimSpace(disposition.Reviewer) == "" || len([]rune(disposition.Reviewer)) > 100 || containsControl(disposition.Reviewer) || !validRFC3339(disposition.ReviewedUTC) {
		return errors.New("disposition reviewer or reviewedUtc is invalid")
	}
	reviewedAt, _ := time.Parse(time.RFC3339, disposition.ReviewedUTC)
	headAt, err := time.Parse(time.RFC3339, report.HeadCommitUTC)
	if err != nil || reviewedAt.Before(headAt) {
		return errors.New("disposition predates the upstream head")
	}
	required := make(map[string]Change)
	for _, change := range report.Changes {
		if change.Classification != Excluded {
			required[change.Path] = change
		}
	}
	seen := make(map[string]bool, len(disposition.Items))
	for _, item := range disposition.Items {
		change, ok := required[item.Path]
		if !ok || seen[item.Path] || item.Classification != change.Classification {
			return fmt.Errorf("disposition item %q is unexpected, duplicated, or misclassified", item.Path)
		}
		seen[item.Path] = true
		if item.Decision != "implemented" && item.Decision != "no_action_required" {
			return fmt.Errorf("disposition item %q has an invalid decision", item.Path)
		}
		if len(item.References) == 0 {
			return fmt.Errorf("disposition item %q requires at least one reference", item.Path)
		}
		for _, reference := range item.References {
			if strings.TrimSpace(reference) == "" || len([]rune(reference)) > 512 || containsControl(reference) {
				return fmt.Errorf("disposition item %q contains an invalid reference", item.Path)
			}
		}
		if !item.ExclusionsReviewed {
			return fmt.Errorf("disposition item %q has not reviewed permanent exclusions", item.Path)
		}
		if change.Classification == ReviewRequired || change.Classification == DependencyRisk {
			if !item.BinaryReviewed || !item.SourceAndLicenseReviewed || !item.APISchemaReviewed {
				return fmt.Errorf("disposition item %q has incomplete manual review gates", item.Path)
			}
		}
	}
	if len(seen) != len(required) {
		return fmt.Errorf("disposition covers %d of %d required changes", len(seen), len(required))
	}
	return nil
}

func containsControl(value string) bool {
	return strings.ContainsAny(value, "\x00\r\n")
}
