package oxgen

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"time"
)

type OSRunner struct{ Input io.Reader }

func (OSRunner) LookPath(name string) (string, error) { return exec.LookPath(name) }

func makeCommand(ctx context.Context, c Command) (*exec.Cmd, context.CancelFunc) {
	timeout := c.Timeout
	if timeout == 0 {
		timeout = 30 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	cmd := exec.CommandContext(ctx, c.Name, c.Args...)
	cmd.Dir = c.Dir
	cmd.Env = append(append(os.Environ(), "PYTHONUTF8=1", "PYTHONIOENCODING=utf-8"), c.Env...)
	return cmd, cancel
}

func (r OSRunner) Capture(ctx context.Context, c Command) (string, error) {
	if c.Timeout == 0 {
		c.Timeout = 12 * time.Second
	}
	cmd, cancel := makeCommand(ctx, c)
	defer cancel()
	var out, stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	err := cmd.Run()
	text := decodeOutput(out.Bytes())
	if err != nil && stderr.Len() > 0 {
		text += "\n" + decodeOutput(stderr.Bytes())
	}
	return text, err
}

func (r OSRunner) Run(ctx context.Context, c Command, out io.Writer) error {
	fmt.Fprintf(out, "\n> %s", c.Name)
	for _, arg := range c.Args {
		fmt.Fprintf(out, " %q", arg)
	}
	fmt.Fprintln(out)
	cmd, cancel := makeCommand(ctx, c)
	defer cancel()
	if c.Interactive {
		cmd.Stdin = r.Input
	}
	cmd.Stdout = out
	cmd.Stderr = out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s: %w", c.Name, err)
	}
	return nil
}
