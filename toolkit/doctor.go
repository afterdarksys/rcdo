package toolkit

import (
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"git-tools/finding"
)

var doctorLookPath = exec.LookPath
var doctorVersion = executeReadOnly

var doctorTools = []struct {
	name        string
	versionArgs []string
	purpose     string
}{
	{"git", []string{"--version"}, "repository operations"},
	{"tofu", []string{"version", "-json"}, "native OpenTofu validation"},
	{"terraform", []string{"version", "-json"}, "native Terraform validation"},
	{"aws", []string{"--version"}, "AWS collection"},
	{"kubectl", nil, "Kubernetes operations"},
	{"spacectl", nil, "Spacelift operations"},
	{"ansible", nil, "Ansible operations"},
	{"pandoc", []string{"--version"}, "DOCX conversion"},
	{"pdftotext", []string{"-v"}, "PDF conversion"},
}

func probeEvidenceDirectory(dir string) error {
	f, err := os.CreateTemp(dir, ".rcdo-doctor-*")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	defer f.Close()
	const sample = "rcdo local storage probe\n"
	if _, err = f.WriteString(sample); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	b, err := os.ReadFile(name)
	if err != nil {
		return err
	}
	if string(b) != sample {
		return fmt.Errorf("storage round-trip mismatch")
	}
	return os.Remove(name)
}
func runDoctor(args []string, stdout, stderr io.Writer) error {
	var versions, sample bool
	var dir string
	_, o, err := parseFlags("doctor", args, stderr, func(fs *flag.FlagSet) *commonOptions {
		var o commonOptions
		addCommonFlags(fs, &o)
		fs.BoolVar(&versions, "versions", false, "execute allowlisted installed CLI version commands")
		fs.BoolVar(&sample, "sample", false, "include an operator reading sample")
		fs.StringVar(&dir, "evidence-dir", "", "probe create/write/read/delete access in this existing directory")
		return &o
	})
	if err != nil {
		return err
	}
	if o.input != "-" || o.policy != "" {
		return fmt.Errorf("doctor does not accept input or suppressed checks")
	}
	r := finding.Report{CompletedChecks: []string{"RCDO version: " + Version, "Local configuration observations only; credential files are not inspected; no settings are changed"}}
	for _, tool := range doctorTools {
		path, e := doctorLookPath(tool.name)
		if e != nil {
			addIAC(&r, "DOC-MISSING", finding.SeverityWarning, "Optional CLI not found", tool.name, "inspect", "local", "Needed for "+tool.purpose+"; install only if this workflow is required")
			continue
		}
		r.CompletedChecks = append(r.CompletedChecks, "CLI found for "+tool.purpose+": "+tool.name+" at "+safeReportText(path))
		if !versions {
			continue
		}
		if len(tool.versionArgs) == 0 {
			r.CompletedChecks = append(r.CompletedChecks, "Version not queried by this adapter: "+tool.name)
			continue
		}
		result := doctorVersion(path, tool.versionArgs...)
		if result.err != nil {
			r.IncompleteChecks = append(r.IncompleteChecks, "Version probe failed for "+tool.name+"; inspect the CLI locally")
			continue
		}
		raw := result.stdout
		if len(raw) == 0 {
			raw = []byte(result.stderr)
		}
		if len(raw) > 4096 {
			raw = raw[:4096]
		}
		value := safeReportText(strings.TrimSpace(string(raw)))
		if value == "" {
			r.IncompleteChecks = append(r.IncompleteChecks, "Version probe returned no text for "+tool.name)
		} else {
			r.CompletedChecks = append(r.CompletedChecks, "Version output for "+tool.name+": "+value)
		}
	}
	for _, name := range []string{"PAGER", "GIT_PAGER", "AWS_PAGER", "LESS", "CLICOLOR_FORCE", "FORCE_COLOR", "AWS_CLI_AUTO_PROMPT"} {
		value, set := os.LookupEnv(name)
		if set && value != "" {
			addIAC(&r, "DOC-SETTING", finding.SeverityWarning, "Review external pager, color or prompt setting", name, "inspect", "local", "A nonempty environment setting was observed; value withheld. Check that external CLI output remains linear and keyboard accessible")
		} else {
			r.CompletedChecks = append(r.CompletedChecks, name+": unset or empty in this process; external CLI defaults and config files are not inferred")
		}
	}
	if _, set := os.LookupEnv("NO_COLOR"); set {
		r.CompletedChecks = append(r.CompletedChecks, "NO_COLOR is present; support varies by external CLI")
	}
	if dir != "" {
		if e := probeEvidenceDirectory(dir); e != nil {
			r.IncompleteChecks = append(r.IncompleteChecks, "Evidence directory create/write/read/delete probe failed; verify path and permissions")
		} else {
			r.CompletedChecks = append(r.CompletedChecks, "Evidence directory temporary-file round-trip succeeded; durable retention and atomic replacement are not certified")
		}
	} else {
		r.CompletedChecks = append(r.CompletedChecks, "Storage not probed; use --evidence-dir with an existing directory")
	}
	if sample {
		r.CompletedChecks = append(r.CompletedChecks, "Operator sample, not a passed usability check: severity critical; environment staging; resource Db_1; next action inspect evidence; uncertainty one host unreachable", "Operator task: read Db_1 exactly, find the next action, and identify the unknown host using your screen reader, magnifier or braille display; record obstacles in docs/accessible-operations/PILOT.md")
	}
	r.CompletedChecks = append(r.CompletedChecks, "Remediation is advisory. This report is not an accessibility certification.")
	return emitReportOptions(stdout, o, r)
}
