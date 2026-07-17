package game

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRunningProcessesUsesPathAndCreationTime(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	candidate := Candidate{Executable: executable, ExeName: filepath.Base(executable)}
	processes, err := RunningProcesses(candidate)
	if err != nil {
		t.Fatal(err)
	}
	wanted := uint32(os.Getpid())
	for _, process := range processes {
		if process.PID == wanted {
			if !process.VerifiedPath || process.CreationTime == 0 {
				t.Fatalf("current process identity incomplete: %+v", process)
			}
			return
		}
	}
	t.Fatalf("current process %d not found in %+v", wanted, processes)
}
