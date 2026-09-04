package toolkit

import (
	"bytes"
	"fmt"
	"os/exec"
)

type commandResult struct {
	stdout []byte
	stderr string
	err    error
}

var executeReadOnly = func(name string, args ...string) commandResult {
	command := exec.Command(name, args...)
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
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
