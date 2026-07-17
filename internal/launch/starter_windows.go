package launch

import (
	"errors"
	"os/exec"
)

type NativeStarter struct{}

func (NativeStarter) Start(request Request) (Process, error) {
	command := exec.Command(request.Candidate.Executable, request.Arguments...)
	command.Dir = request.Candidate.Root
	if err := command.Start(); err != nil {
		return nil, err
	}
	return &nativeProcess{command: command}, nil
}

type nativeProcess struct{ command *exec.Cmd }

func (p *nativeProcess) PID() int { return p.command.Process.Pid }

func (p *nativeProcess) Wait() (int, error) {
	err := p.command.Wait()
	if p.command.ProcessState == nil {
		return -1, err
	}
	var exitError *exec.ExitError
	if errors.As(err, &exitError) {
		return p.command.ProcessState.ExitCode(), nil
	}
	return p.command.ProcessState.ExitCode(), err
}
