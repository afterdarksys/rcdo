package toolkit

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/user"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

type auditConfig struct {
	Enabled        bool   `json:"enabled" yaml:"enabled"`
	File           string `json:"file" yaml:"file"`
	Output         string `json:"output" yaml:"output"`
	MaxOutputBytes int    `json:"max_output_bytes" yaml:"max_output_bytes"`
}

func validateAuditConfig(c auditConfig) error {
	if c.Output != "" && !oneOf(c.Output, "redacted", "none") {
		return fmt.Errorf("audit.output must be redacted or none")
	}
	if c.MaxOutputBytes < 0 || c.MaxOutputBytes > 1<<20 {
		return fmt.Errorf("audit.max_output_bytes must be 0 (default) or 1..1048576")
	}
	if strings.TrimSpace(c.File) != c.File || strings.ContainsAny(c.File, "\x00\r\n") {
		return fmt.Errorf("invalid audit.file")
	}
	return nil
}

type auditRecord struct {
	SchemaVersion      string    `json:"schema_version"`
	RunID              string    `json:"run_id"`
	Event              string    `json:"event"`
	At                 time.Time `json:"at"`
	Command            string    `json:"command"`
	Arguments          []string  `json:"arguments,omitempty"`
	EffectiveArguments []string  `json:"effective_arguments,omitempty"`
	ConfigFile         string    `json:"config_file"`
	User               string    `json:"user"`
	UID                string    `json:"uid"`
	EffectiveUID       int       `json:"effective_uid"`
	Host               string    `json:"host"`
	PID                int       `json:"pid"`
	WorkingDirectory   string    `json:"working_directory"`
	ExitCode           *int      `json:"exit_code,omitempty"`
	DurationMS         int64     `json:"duration_ms,omitempty"`
	OutputMode         string    `json:"output_mode"`
	Stdout             string    `json:"stdout,omitempty"`
	Stderr             string    `json:"stderr,omitempty"`
	StdoutBytes        int64     `json:"stdout_bytes"`
	StderrBytes        int64     `json:"stderr_bytes"`
	StdoutTruncated    bool      `json:"stdout_truncated"`
	StderrTruncated    bool      `json:"stderr_truncated"`
}

// Run captures the invocation at the common entry point, including aliases,
// help, argument failures and commands that do not use configurable defaults.
func Run(command string, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	name, commandArgs := command, args
	if oneOf(name, "", "rcdo", "git-tools") {
		name = "help"
		if len(args) > 0 {
			name, commandArgs = args[0], args[1:]
		}
	}
	_, path, _ := extractRuntimeConfigFlag(commandArgs)
	if name == "config" {
		// The config command selects its target with --file.
		for i, arg := range commandArgs {
			if oneOf(arg, "--file", "-file") && i+1 < len(commandArgs) {
				path = commandArgs[i+1]
			}
			if strings.HasPrefix(arg, "--file=") || strings.HasPrefix(arg, "-file=") {
				path = strings.SplitN(arg, "=", 2)[1]
			}
		}
	}
	if path == "" {
		var err error
		path, err = defaultAppConfigPath()
		if err != nil {
			fmt.Fprintln(stderr, "error: cannot resolve audit configuration")
			return 2
		}
	}
	config, found, err := loadAppConfig(path)
	if err != nil {
		fmt.Fprintln(stderr, "error: cannot load runtime configuration:", err)
		return 2
	}
	if !found || !config.Audit.Enabled {
		return runCommand(command, args, stdin, stdout, stderr)
	}
	c := config.Audit
	if c.File == "" {
		c.File = "audit.jsonl"
	}
	if c.Output == "" {
		c.Output = "redacted"
	}
	if c.MaxOutputBytes == 0 {
		c.MaxOutputBytes = 65536
	}
	if !filepath.IsAbs(c.File) {
		c.File = filepath.Join(filepath.Dir(path), c.File)
	}
	c.File, err = filepath.Abs(c.File)
	if err != nil {
		fmt.Fprintln(stderr, "error: invalid audit path")
		return 2
	}
	// Avoid appending audit records to a config, credential, evidence or state file.
	candidates := append([]string{path, credentialsPath(path, config)}, commandArgs...)
	for _, values := range config.Commands {
		for key, value := range values {
			if oneOf(normalizeConfigFlag(key), "state", "registry", "policy", "repo-policy") {
				candidates = append(candidates, fmt.Sprint(value))
			}
		}
	}
	for _, candidate := range candidates {
		if strings.HasPrefix(candidate, "-") {
			if _, value, ok := strings.Cut(candidate, "="); ok {
				candidate = value
			} else {
				continue
			}
		}
		if candidate != "" && separateArtifact(candidate, boundArtifact{Path: c.File}) != nil {
			fmt.Fprintln(stderr, "error: audit file must be separate from command files")
			return 2
		}
	}
	for _, stream := range []any{stdin, stdout, stderr} {
		if f, ok := stream.(*os.File); ok {
			streamInfo, streamErr := f.Stat()
			auditInfo, auditErr := os.Stat(c.File)
			if streamErr == nil && auditErr == nil && os.SameFile(streamInfo, auditInfo) {
				fmt.Fprintln(stderr, "error: audit file must be separate from standard streams")
				return 2
			}
		}
	}
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		fmt.Fprintln(stderr, "error: cannot create audit run identity")
		return 2
	}
	identity, err := user.Current()
	if err != nil {
		fmt.Fprintln(stderr, "error: cannot determine audit user identity")
		return 2
	}
	host, err := os.Hostname()
	if err != nil {
		fmt.Fprintln(stderr, "error: cannot determine audit host")
		return 2
	}
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(stderr, "error: cannot determine audit working directory")
		return 2
	}
	startedClock := time.Now()
	started := startedClock.UTC()
	r := auditRecord{SchemaVersion: "1", RunID: hex.EncodeToString(id[:]), Event: "start", At: started, Command: auditText(name), Arguments: auditArguments(commandArgs), ConfigFile: auditText(path), User: auditText(identity.Username), UID: identity.Uid, EffectiveUID: os.Geteuid(), Host: auditText(host), PID: os.Getpid(), WorkingDirectory: auditText(cwd), OutputMode: c.Output}
	if err := appendAudit(c.File, r); err != nil {
		fmt.Fprintln(stderr, "error: audit start could not be saved; command not run:", err)
		return 2
	}
	out, errors := &auditCapture{destination: stdout, limit: c.MaxOutputBytes}, &auditCapture{destination: stderr, limit: c.MaxOutputBytes}
	if c.Output == "none" {
		out.limit, errors.limit = 0, 0
	}
	code := runCommandObserved(command, args, stdin, out, errors, func(effective []string) { r.EffectiveArguments = auditArguments(effective) })
	r.Event, r.At, r.ExitCode = "finish", time.Now().UTC(), &code
	r.DurationMS = time.Since(startedClock).Milliseconds()
	r.Stdout, r.StdoutBytes, r.StdoutTruncated = out.snapshot()
	r.Stderr, r.StderrBytes, r.StderrTruncated = errors.snapshot()
	if err := appendAudit(c.File, r); err != nil {
		fmt.Fprintf(stderr, "error: command exited %d but audit completion could not be saved: %v\n", code, err)
		return 2
	}
	return code
}

type auditCapture struct {
	mu          sync.Mutex
	destination io.Writer
	limit       int
	total       int64
	buffer      bytes.Buffer
}

func (w *auditCapture) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	n, err := w.destination.Write(p)
	w.total += int64(n)
	keep := min(n, w.limit-w.buffer.Len())
	if keep > 0 {
		w.buffer.Write(p[:keep])
	}
	return n, err
}

func (w *auditCapture) snapshot() (string, int64, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	truncated := w.total > int64(w.buffer.Len())
	if w.limit == 0 {
		return "", w.total, false
	}
	// Withhold an incomplete final line rather than store a cut secret assignment
	// or half a JSON string. Redaction runs after all writes have been captured.
	data := w.buffer.Bytes()
	if truncated {
		if end := bytes.LastIndexByte(data, '\n'); end >= 0 {
			data = data[:end+1]
		} else {
			data = nil
		}
	}
	result := auditText(string(data))
	if len(result) > w.limit {
		return "", w.total, true
	}
	return result, w.total, truncated
}

var auditURLCredentials = regexp.MustCompile(`([a-zA-Z][a-zA-Z0-9+.-]*://)[^/\s@]+@`)

func auditText(value string) string {
	// Mask structured secret fields even if their values are arrays or objects.
	if json.Valid([]byte(value)) {
		var object any
		decoder := json.NewDecoder(strings.NewReader(value))
		decoder.UseNumber()
		if decoder.Decode(&object) == nil && redactAuditObject(object) {
			if data, err := json.Marshal(object); err == nil {
				value = string(data)
			}
		}
	}
	// An unfinished private-key block must not evade the complete-block redactor.
	if start := strings.Index(value, "-----BEGIN "); start >= 0 && strings.Contains(value[start:], "PRIVATE KEY") {
		value = value[:start] + "[REDACTED-PRIVATE-MATERIAL]"
	}
	return markdownSafe(redactAIText(auditURLCredentials.ReplaceAllString(value, "${1}[REDACTED]@")))
}

func redactAuditObject(value any) bool {
	changed := false
	switch node := value.(type) {
	case map[string]any:
		for key, child := range node {
			if isSensitivePath(key) || strings.EqualFold(key, "authorization") {
				node[key], changed = "[REDACTED]", true
			} else {
				changed = redactAuditObject(child) || changed
			}
		}
	case []any:
		for _, child := range node {
			changed = redactAuditObject(child) || changed
		}
	}
	return changed
}

func auditArguments(args []string) []string {
	result := make([]string, 0, len(args))
	hideNext := false
	for _, arg := range args {
		if hideNext {
			result = append(result, "[REDACTED]")
			hideNext = false
			continue
		}
		if strings.HasPrefix(arg, "-") {
			key, _, equals := strings.Cut(strings.TrimLeft(arg, "-"), "=")
			if isSensitivePath(key) || oneOf(key, "value", "text", "question", "authorization", "header") {
				if equals {
					result = append(result, "--"+key+"=[REDACTED]")
				} else {
					result = append(result, auditText(arg))
					hideNext = true
				}
				continue
			}
		}
		result = append(result, auditText(arg))
	}
	return result
}

// A short-lived directory lock coordinates appenders across processes without
// serializing command execution. Never reclaim another process's lock implicitly.
func appendAudit(path string, r auditRecord) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("cannot create audit directory")
	}
	lock := path + ".lock"
	deadline := time.Now().Add(2 * time.Second)
	for {
		err := os.Mkdir(lock, 0700)
		if err == nil {
			break
		}
		if !os.IsExist(err) || time.Now().After(deadline) {
			return fmt.Errorf("audit append lock unavailable")
		}
		time.Sleep(10 * time.Millisecond)
	}
	defer os.Remove(lock)
	info, err := os.Lstat(path)
	var f *os.File
	if os.IsNotExist(err) {
		f, err = os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR|os.O_APPEND, 0600)
	} else if err == nil {
		if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
			return fmt.Errorf("audit file must be a private regular file (0600)")
		}
		f, err = os.OpenFile(path, os.O_RDWR|os.O_APPEND, 0600)
	} else {
		return fmt.Errorf("cannot inspect audit file")
	}
	if err != nil {
		return fmt.Errorf("cannot open audit file")
	}
	defer f.Close()
	current, err := f.Stat()
	if err != nil || !current.Mode().IsRegular() || current.Mode().Perm()&0077 != 0 || info != nil && !os.SameFile(info, current) {
		return fmt.Errorf("audit file changed or is not private")
	}
	if current.Size() > 0 {
		var last [1]byte
		if _, err := f.ReadAt(last[:], current.Size()-1); err != nil || last[0] != '\n' {
			return fmt.Errorf("audit file ends with an incomplete record; preserve and repair it before continuing")
		}
	}
	data, err := json.Marshal(r)
	if err != nil {
		return fmt.Errorf("cannot encode audit event")
	}
	data = append(data, '\n')
	if n, err := f.Write(data); err != nil || n != len(data) {
		return fmt.Errorf("cannot append audit event")
	}
	if err := f.Sync(); err != nil {
		return fmt.Errorf("cannot sync audit event")
	}
	return f.Close()
}
