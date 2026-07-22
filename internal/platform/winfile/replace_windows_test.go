package winfile

import (
	"errors"
	"testing"

	"golang.org/x/sys/windows"
)

func TestReplaceRetriesOnlyTransientWindowsErrors(t *testing.T) {
	original := moveFileEx
	t.Cleanup(func() { moveFileEx = original })

	calls := 0
	moveFileEx = func(_, _ *uint16, _ uint32) error {
		calls++
		if calls < 3 {
			return windows.ERROR_SHARING_VIOLATION
		}
		return nil
	}
	if err := Replace(nil, nil, 0); err != nil || calls != 3 {
		t.Fatalf("transient replace err=%v calls=%d", err, calls)
	}

	permanent := errors.New("permanent")
	calls = 0
	moveFileEx = func(_, _ *uint16, _ uint32) error {
		calls++
		return permanent
	}
	if err := Replace(nil, nil, 0); !errors.Is(err, permanent) || calls != 1 {
		t.Fatalf("permanent replace err=%v calls=%d", err, calls)
	}
}
