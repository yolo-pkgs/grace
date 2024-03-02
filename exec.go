//go:build unix
// +build unix

package grace

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"time"

	"golang.org/x/sys/unix"
)

var (
	ErrTimeout    = errors.New("timeout")
	ErrFailToKill = errors.New("failed to kill process")
)

type Output struct {
	StdOut   string
	StdErr   string
	ExitCode int
}

func (o Output) Combine() string {
	return o.StdOut + o.StdErr
}

// Command is wrapped in single quotes!
func RunTimedSh(timeout time.Duration, command string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	output, err := Spawn(ctx, exec.Command("sh", "-c", command))
	if err != nil {
		return "", err
	}
	return output.Combine(), nil
}

func RunTimed(timeout time.Duration, env []string, cmd string, args ...string) (Output, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	command := exec.Command(cmd, args...)
	command.Env = append(os.Environ(), env...)

	output, err := Spawn(ctx, command)
	if err != nil {
		return Output{}, err
	}

	return output, nil
}

func Spawn(ctx context.Context, cmd *exec.Cmd) (Output, error) {
	cmd.SysProcAttr = &unix.SysProcAttr{Setpgid: true}

	stdOut, err := cmd.StdoutPipe()
	if err != nil {
		return Output{}, err
	}

	stdErr, err := cmd.StderrPipe()
	if err != nil {
		return Output{}, err
	}

	if err = cmd.Start(); err != nil {
		return Output{}, err
	}

	outChan := make(chan Output, 1)
	errChan := make(chan error, 1)

	go func() {
		stdOutput, err := io.ReadAll(stdOut)
		if err != nil {
			errChan <- err
		}

		stdError, err := io.ReadAll(stdErr)
		if err != nil {
			errChan <- err
		}

		exitCode := 0

		if err := cmd.Wait(); err != nil {
			if exitError, ok := err.(*exec.ExitError); ok {
				exitCode = exitError.ExitCode()
			} else {
				errChan <- err
				return
			}
		}

		outChan <- Output{
			StdOut:   string(stdOutput),
			StdErr:   string(stdError),
			ExitCode: exitCode,
		}
	}()

	select {
	case output := <-outChan:
		return output, nil
	case err := <-errChan:
		return Output{}, err
	case <-ctx.Done():
		if err := unix.Kill(-cmd.Process.Pid, unix.SIGTERM); err == nil {
			return Output{}, ErrTimeout
		}

		err := unix.Kill(-cmd.Process.Pid, unix.SIGKILL)
		if err == nil {
			return Output{}, ErrTimeout
		}

		return Output{}, errors.Join(err, ErrTimeout, ErrFailToKill)
	}
}
