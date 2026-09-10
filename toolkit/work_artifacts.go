package toolkit

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

type boundArtifact struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

func captureArtifact(path string) (boundArtifact, []byte, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return boundArtifact{}, nil, err
	}
	data, err := readConfigSource(absolute)
	if err != nil {
		return boundArtifact{}, nil, err
	}
	return boundArtifact{absolute, digestBytes(data)}, data, nil
}
func readBoundArtifact(a boundArtifact) ([]byte, error) {
	if !filepath.IsAbs(a.Path) || len(a.SHA256) != 64 {
		return nil, fmt.Errorf("invalid artifact binding")
	}
	data, err := readConfigSource(a.Path)
	if err != nil {
		return nil, fmt.Errorf("bound artifact unavailable")
	}
	if digestBytes(data) != a.SHA256 {
		return nil, fmt.Errorf("bound artifact changed; establish a new evidence version")
	}
	return data, nil
}
func separateArtifact(path string, a boundArtifact) error {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	if absolute == a.Path {
		return fmt.Errorf("state must be separate from source evidence")
	}
	p, pe := os.Stat(path)
	q, qe := os.Stat(a.Path)
	if pe == nil && qe == nil && os.SameFile(p, q) {
		return fmt.Errorf("state aliases source evidence")
	}
	return nil
}
func saveWorkState(path string, value any, previous []byte, check func() error) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	verify := func() error {
		if check != nil {
			if err := check(); err != nil {
				return err
			}
		}
		if previous != nil {
			current, err := readConfigSource(path)
			if err != nil || !bytes.Equal(current, previous) {
				return fmt.Errorf("state changed concurrently")
			}
		}
		return nil
	}
	if err := verify(); err != nil {
		return err
	}
	if previous == nil {
		return publishMarkdown(path, data)
	}
	return atomicReplaceChecked(path, data, io.Discard, verify)
}
