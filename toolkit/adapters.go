package toolkit

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"time"
)

type commandResult struct {
	stdout []byte
	stderr string
	err    error
}

var executeReadOnly = func(name string, args ...string) commandResult {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, name, args...)
	command.WaitDelay = time.Second
	stdout := limitedCommandBuffer{limit: 32 << 20}
	stderr := limitedCommandBuffer{limit: 64 << 10}
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	if ctx.Err() != nil {
		err = fmt.Errorf("collector timed out")
	}
	if stdout.exceeded || stderr.exceeded {
		err = fmt.Errorf("collector output exceeded limit")
	}
	return commandResult{stdout: stdout.Bytes(), stderr: stderr.String(), err: err}
}

func collectJSON(name string, args ...string) ([]byte, error) {
	result := executeReadOnly(name, args...)
	if result.err != nil {
		return nil, fmt.Errorf("%s collection failed: %v; stderr: %s", name, result.err, result.stderr)
	}
	if len(bytes.TrimSpace(result.stdout)) == 0 {
		return nil, fmt.Errorf("%s collection returned empty output", name)
	}
	return result.stdout, nil
}

// Drain excess output without retaining it, so a full pipe cannot deadlock a child.
type limitedCommandBuffer struct {
	buffer   bytes.Buffer
	limit    int
	exceeded bool
}

func (b *limitedCommandBuffer) Write(p []byte) (int, error) {
	n := len(p)
	remaining := b.limit - b.buffer.Len()
	if n > remaining {
		b.exceeded = true
		p = p[:remaining]
	}
	_, err := b.buffer.Write(p)
	return n, err
}

func (b *limitedCommandBuffer) Bytes() []byte  { return b.buffer.Bytes() }
func (b *limitedCommandBuffer) String() string { return b.buffer.String() }
