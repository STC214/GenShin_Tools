package winfile

import (
	"errors"
	"time"

	"golang.org/x/sys/windows"
)

var moveFileEx = windows.MoveFileEx

// Replace atomically moves source over destination. Antivirus and indexers can
// briefly hold a just-written file on Windows, so retry only transient sharing,
// lock and access errors with a strictly bounded backoff (310 ms total).
func Replace(source, destination *uint16, flags uint32) error {
	var moveErr error
	for attempt := 0; attempt < 6; attempt++ {
		moveErr = moveFileEx(source, destination, flags)
		if moveErr == nil {
			return nil
		}
		if !errors.Is(moveErr, windows.ERROR_ACCESS_DENIED) && !errors.Is(moveErr, windows.ERROR_SHARING_VIOLATION) && !errors.Is(moveErr, windows.ERROR_LOCK_VIOLATION) {
			return moveErr
		}
		if attempt < 5 {
			time.Sleep((10 * time.Millisecond) << attempt)
		}
	}
	return moveErr
}
