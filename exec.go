//go:build unix
// +build unix

package grace

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os/exec"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

var (
	ErrTimeout    = errors.New("timeout")
	ErrFailToKill = errors.New("failed to kill process")
)

type Output struct {
	StdOut string
	StdErr string
}

func (o Output) Combine() string {
	return o.StdOut + o.StdErr
}

func RunShTimed(command string, timeout time.Duration) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	output, err := Spawn(ctx, exec.Command("sh", "-c", fmt.Sprintf(`'%s'`, command)))
	if err != nil {
		return "", err
	}
	return output.Combine(), nil
}

func RunTimed(command string, timeout time.Duration) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	commandFields := strings.Fields(command)
	args := make([]string, 0)
	if len(commandFields) > 1 {
		args = commandFields[1:]
	}
	bin := commandFields[0]

	output, err := Spawn(ctx, exec.Command(bin, args...))
	if err != nil {
		return "", err
	}
	return output.Combine(), nil
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

		if err := cmd.Wait(); err != nil {
			errChan <- err
		}

		outChan <- Output{
			StdOut: string(stdOutput),
			StdErr: string(stdError),
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
		slog.Warn("sending SIGKILL", slog.Int("pid", cmd.Process.Pid), slog.String("cmd", cmd.String()))
		err := unix.Kill(-cmd.Process.Pid, unix.SIGKILL)
		if err == nil {
			return Output{}, ErrTimeout
		}

		return Output{}, errors.Join(err, ErrTimeout, ErrFailToKill)
	}
}
