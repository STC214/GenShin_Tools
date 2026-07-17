package launch

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"genshintools/internal/game"
)

func TestBuildArgumentsAndWindowsQuoting(t *testing.T) {
	config := Config{Width: 2560, Height: 1440, Monitor: 2, WindowMode: WindowBorderless, CustomArguments: `-logFile "C:\日志 目录\output.log" --name "A B"`}
	got, err := BuildArguments(config)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"-screen-width", "2560", "-screen-height", "1440", "-screen-fullscreen", "0", "-window-mode", "borderless", "-popupwindow", "-monitor", "2", "-logFile", `C:\日志 目录\output.log`, "--name", "A B"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("arguments = %#v, want %#v", got, want)
	}
}

func TestConfigValidation(t *testing.T) {
	for _, config := range []Config{
		{Width: 1920},
		{Width: 100, Height: 100},
		{Width: 1920, Height: 1080, Monitor: -1},
		{Width: 1920, Height: 1080, WindowMode: 99},
		{Width: 1920, Height: 1080, CustomArguments: "bad\x00arg"},
	} {
		if _, err := config.Normalized(); err == nil {
			t.Fatalf("invalid config accepted: %+v", config)
		}
	}
}

type fakeProcess struct {
	pid     int
	exit    chan struct{}
	code    int
	err     error
	waiting chan struct{}
}

func (p *fakeProcess) PID() int { return p.pid }
func (p *fakeProcess) Wait() (int, error) {
	close(p.waiting)
	<-p.exit
	return p.code, p.err
}

type fakeStarter struct {
	mu      sync.Mutex
	process *fakeProcess
	err     error
	request Request
	starts  int
}

func (s *fakeStarter) Start(request Request) (Process, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.starts++
	s.request = request
	return s.process, s.err
}

func TestEngineLaunchExitAndCloseNeverKills(t *testing.T) {
	candidate := testCandidate(t)
	process := &fakeProcess{pid: 4321, exit: make(chan struct{}), waiting: make(chan struct{})}
	starter := &fakeStarter{process: process}
	updates := make(chan Snapshot, 8)
	engine, err := NewEngine(starter, func(game.Candidate) ([]game.ProcessIdentity, error) { return nil, nil }, func(snapshot Snapshot) { updates <- snapshot })
	if err != nil {
		t.Fatal(err)
	}
	if err := engine.Launch(candidate, DefaultConfig()); err != nil {
		t.Fatal(err)
	}
	<-process.waiting
	if snapshot := engine.Snapshot(); snapshot.State != StateRunning || snapshot.PID != 4321 || !snapshot.Owned {
		t.Fatalf("running snapshot = %+v", snapshot)
	}
	engine.Close()
	select {
	case <-process.exit:
		t.Fatal("Close terminated the game process")
	default:
	}
	close(process.exit)
}

func TestEngineFastExitAndStartFailureRecover(t *testing.T) {
	candidate := testCandidate(t)
	process := &fakeProcess{pid: 7, exit: make(chan struct{}), waiting: make(chan struct{}), code: 23}
	starter := &fakeStarter{process: process}
	engine, _ := NewEngine(starter, func(game.Candidate) ([]game.ProcessIdentity, error) { return nil, nil }, nil)
	if err := engine.Launch(candidate, DefaultConfig()); err != nil {
		t.Fatal(err)
	}
	close(process.exit)
	deadline := time.Now().Add(time.Second)
	for engine.Snapshot().State != StateExited && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if snapshot := engine.Snapshot(); snapshot.State != StateExited || snapshot.ExitCode != 23 {
		t.Fatalf("exit snapshot = %+v", snapshot)
	}
	starter.err = errors.New("access denied")
	if err := engine.Launch(candidate, DefaultConfig()); err == nil {
		t.Fatal("start failure reported success")
	}
	if snapshot := engine.Snapshot(); snapshot.State != StateFailed || snapshot.LastError == "" {
		t.Fatalf("failure snapshot = %+v", snapshot)
	}
}

func TestEngineRefusesExistingAndConcurrentLaunch(t *testing.T) {
	candidate := testCandidate(t)
	starter := &fakeStarter{process: &fakeProcess{pid: 1, exit: make(chan struct{}), waiting: make(chan struct{})}}
	engine, _ := NewEngine(starter, func(game.Candidate) ([]game.ProcessIdentity, error) { return []game.ProcessIdentity{{PID: 99}}, nil }, nil)
	if err := engine.Launch(candidate, DefaultConfig()); !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("existing process error = %v", err)
	}
	if starter.starts != 0 {
		t.Fatal("starter called for existing process")
	}
	engine, _ = NewEngine(starter, func(game.Candidate) ([]game.ProcessIdentity, error) { return nil, nil }, nil)
	if err := engine.Launch(candidate, DefaultConfig()); err != nil {
		t.Fatal(err)
	}
	if err := engine.Launch(candidate, DefaultConfig()); err == nil {
		t.Fatal("concurrent launch accepted")
	}
	close(starter.process.exit)
}

func TestEngineLaunchWithStarterUsesOverride(t *testing.T) {
	candidate := testCandidate(t)
	defaultStarter := &fakeStarter{}
	process := &fakeProcess{pid: 77, exit: make(chan struct{}), waiting: make(chan struct{})}
	override := &fakeStarter{process: process}
	engine, err := NewEngine(defaultStarter, func(game.Candidate) ([]game.ProcessIdentity, error) { return nil, nil }, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := engine.LaunchWithStarter(candidate, DefaultConfig(), override); err != nil {
		t.Fatal(err)
	}
	if defaultStarter.starts != 0 || override.starts != 1 || engine.Snapshot().PID != 77 {
		t.Fatalf("default starts=%d override starts=%d snapshot=%+v", defaultStarter.starts, override.starts, engine.Snapshot())
	}
	close(process.exit)
}

func testCandidate(t *testing.T) game.Candidate {
	t.Helper()
	root := t.TempDir()
	executable := filepath.Join(root, "原神 Test.exe")
	if err := os.WriteFile(executable, []byte("fixture"), 0o644); err != nil {
		t.Fatal(err)
	}
	return game.Candidate{Root: root, Executable: executable, ExeName: filepath.Base(executable)}
}
