package toolkit

import (
	"archive/zip"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"git-tools/finding"
)

type workflowOrigin struct {
	Provider string `json:"provider"`
	Project  string `json:"project"`
	RunID    string `json:"run_id"`
	Attempt  int    `json:"attempt"`
}
type workflowProfile struct {
	SchemaVersion string           `json:"schema_version"`
	Identity      workflowIdentity `json:"identity"`
	Origin        workflowOrigin   `json:"origin"`
	Bundle        *boundArtifact   `json:"bundle,omitempty"`
	ArtifactID    string           `json:"artifact_id,omitempty"`
	Adapter       string           `json:"adapter,omitempty"`
	Endpoint      string           `json:"endpoint,omitempty"`
	Stage         string           `json:"stage"`
}
type workflowExport struct {
	SchemaVersion string           `json:"schema_version"`
	Identity      workflowIdentity `json:"identity"`
	Origin        workflowOrigin   `json:"origin"`
	Archive       string           `json:"archive_base64"`
	SHA256        string           `json:"sha256"`
}

var githubProjectPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)
var decimalID = regexp.MustCompile(`^[0-9]+$`)

func collectGithubBundle(p workflowProfile) ([]byte, error) {
	if !githubProjectPattern.MatchString(p.Origin.Project) || !decimalID.MatchString(p.Origin.RunID) || !decimalID.MatchString(p.ArtifactID) || p.Origin.Attempt < 1 {
		return nil, fmt.Errorf("GitHub collection requires repository, numeric run/artifact IDs and attempt")
	}
	runPath := "repos/" + p.Origin.Project + "/actions/runs/" + p.Origin.RunID
	inspect := func() error {
		result := executeReadOnly("gh", "api", runPath)
		if result.err != nil {
			return fmt.Errorf("GitHub run acquisition failed; diagnostics withheld")
		}
		var run struct {
			ID         json.Number `json:"id"`
			HeadSHA    string      `json:"head_sha"`
			Status     string      `json:"status"`
			Conclusion string      `json:"conclusion"`
			Attempt    int         `json:"run_attempt"`
		}
		if collectionJSON(result.stdout, &run) != nil || string(run.ID) != p.Origin.RunID || run.HeadSHA != p.Identity.Commit || run.Status != "completed" || run.Conclusion != "success" || run.Attempt != p.Origin.Attempt {
			return fmt.Errorf("GitHub run identity, commit, attempt or successful completion does not match")
		}
		return nil
	}
	if err := inspect(); err != nil {
		return nil, err
	}
	artifactPath := "repos/" + p.Origin.Project + "/actions/artifacts/" + p.ArtifactID
	meta := executeReadOnly("gh", "api", artifactPath)
	if meta.err != nil {
		return nil, fmt.Errorf("GitHub artifact metadata unavailable")
	}
	var artifact struct {
		ID      json.Number `json:"id"`
		Expired bool        `json:"expired"`
		Digest  string      `json:"digest"`
		Size    int64       `json:"size_in_bytes"`
		Run     struct {
			ID      json.Number `json:"id"`
			HeadSHA string      `json:"head_sha"`
		} `json:"workflow_run"`
	}
	if collectionJSON(meta.stdout, &artifact) != nil || string(artifact.ID) != p.ArtifactID || artifact.Expired || artifact.Size <= 0 || artifact.Size > 32<<20 || string(artifact.Run.ID) != p.Origin.RunID || artifact.Run.HeadSHA != p.Identity.Commit || !strings.HasPrefix(artifact.Digest, "sha256:") {
		return nil, fmt.Errorf("GitHub artifact identity, expiry, size or digest is invalid")
	}
	archive := executeReadOnly("gh", "api", artifactPath+"/zip")
	if archive.err != nil || "sha256:"+digestBytes(archive.stdout) != artifact.Digest {
		return nil, fmt.Errorf("GitHub artifact download failed or digest differs")
	}
	if err := inspect(); err != nil {
		return nil, err
	}
	return archive.stdout, nil
}
func collectAdapterBundle(p workflowProfile) ([]byte, error) {
	if !filepath.IsAbs(p.Adapter) {
		return nil, fmt.Errorf("adapter must be an explicit absolute executable path")
	}
	result := executeReadOnly(p.Adapter, "rcdo-export", "--project", p.Origin.Project, "--run", p.Origin.RunID)
	if result.err != nil {
		return nil, fmt.Errorf("export adapter failed; diagnostics withheld")
	}
	var export workflowExport
	if workflowJSON(result.stdout, &export) != nil || export.SchemaVersion != "1" || export.Identity != p.Identity || export.Origin != p.Origin {
		return nil, fmt.Errorf("adapter export identity differs from profile")
	}
	data, e := base64.StdEncoding.DecodeString(export.Archive)
	if e != nil || len(data) > 32<<20 || digestBytes(data) != export.SHA256 {
		return nil, fmt.Errorf("adapter archive is invalid or changed")
	}
	return data, nil
}
func inspectSpaceOrigin(p workflowProfile) error {
	c := &contextAcquirer{}
	values, e := acquireSpaceContext(c, p.Origin.Project, p.Origin.RunID, p.Endpoint)
	if e != nil {
		return e
	}
	if values["commit"] != p.Identity.Commit || values["state"] != "FINISHED" || values["needs_approval"] != "false" {
		return fmt.Errorf("Spacelift run commit or successful state does not match")
	}
	return nil
}
func readWorkflowArchive(data []byte) (map[string][]byte, error) {
	if len(data) > 32<<20 {
		return nil, fmt.Errorf("archive exceeds 32 MiB")
	}
	reader, e := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if e != nil {
		return nil, fmt.Errorf("invalid workflow ZIP")
	}
	if len(reader.File) > 256 {
		return nil, fmt.Errorf("too many archive entries")
	}
	files := map[string][]byte{}
	total := int64(0)
	for _, f := range reader.File {
		name := f.Name
		if f.FileInfo().IsDir() {
			continue
		}
		if name == "" || strings.Contains(name, "\\") || filepath.IsAbs(name) || filepath.ToSlash(filepath.Clean(name)) != name || strings.HasPrefix(name, "../") || name == ".." || f.Mode()&os.ModeSymlink != 0 || !f.Mode().IsRegular() {
			return nil, fmt.Errorf("unsafe workflow archive path or file type")
		}
		if _, ok := files[name]; ok {
			return nil, fmt.Errorf("duplicate archive file")
		}
		if f.UncompressedSize64 > 16<<20 {
			return nil, fmt.Errorf("archive file exceeds 16 MiB")
		}
		stream, e := f.Open()
		if e != nil {
			return nil, e
		}
		b, e := io.ReadAll(io.LimitReader(stream, (16<<20)+1))
		stream.Close()
		total += int64(len(b))
		if e != nil || len(b) > 16<<20 || total > 32<<20 {
			return nil, fmt.Errorf("archive extraction incomplete or oversized")
		}
		files[name] = b
	}
	if files["workflow.json"] == nil {
		return nil, fmt.Errorf("archive requires root workflow.json")
	}
	return files, nil
}
func validateExportFiles(files map[string][]byte, p workflowProfile) error {
	var m workflowManifest
	if workflowJSON(files["workflow.json"], &m) != nil || m.Identity != p.Identity || m.Origin == nil || *m.Origin != p.Origin {
		return fmt.Errorf("export manifest identity or origin does not match profile")
	}
	read := func(a boundArtifact) ([]byte, error) {
		if filepath.IsAbs(a.Path) {
			return nil, fmt.Errorf("export artifact paths must be relative")
		}
		b, ok := files[a.Path]
		if !ok || digestBytes(b) != a.SHA256 {
			return nil, fmt.Errorf("export artifact missing or hash mismatch")
		}
		return b, nil
	}
	for _, a := range []boundArtifact{m.Outputs, m.Producer, m.Inventory, m.Playbook} {
		if _, e := read(a); e != nil {
			return e
		}
	}
	for _, a := range []*boundArtifact{m.ExtraVars, m.Events, m.Verification, m.Execution, m.SourceGraph} {
		if a != nil {
			if _, e := read(*a); e != nil {
				return e
			}
		}
	}
	if m.Execution != nil {
		b, _ := read(*m.Execution)
		var x workflowExecution
		if workflowJSON(b, &x) != nil {
			return fmt.Errorf("invalid invocation export")
		}
		if x.ResolvedInputs != nil {
			if _, e := read(*x.ResolvedInputs); e != nil {
				return e
			}
		}
	}
	if m.SourceGraph != nil {
		b, _ := read(*m.SourceGraph)
		var g workflowSourceGraph
		if workflowJSON(b, &g) != nil {
			return fmt.Errorf("invalid source graph export")
		}
		if _, e := read(g.Root); e != nil {
			return e
		}
		for _, a := range g.Files {
			if _, e := read(a); e != nil {
				return e
			}
		}
	}
	return nil
}

func runWorkflowCollect(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	var profilePath, out string
	var native bool
	_, o, e := parseFlags("workflow-collect", args, stderr, func(fs *flag.FlagSet) *commonOptions {
		var o commonOptions
		addCommonFlags(fs, &o)
		fs.StringVar(&profilePath, "profile", "", "workplace collection profile")
		fs.StringVar(&out, "output-dir", "", "new private evidence directory")
		fs.BoolVar(&native, "native", false, "query installed CLIs or explicit export adapter")
		return &o
	})
	if e != nil {
		return e
	}
	if profilePath == "" || out == "" {
		return fmt.Errorf("profile and output-dir are required")
	}
	raw, e := readConfigSource(profilePath)
	if e != nil {
		return e
	}
	var p workflowProfile
	if workflowJSON(raw, &p) != nil || p.SchemaVersion != "1" || !workflowLabels(p.Identity) || !oneOf(p.Origin.Provider, "local", "github", "spacelift", "custom") || !operationLabel(p.Origin.Project) || !operationLabel(p.Origin.RunID) || p.Origin.Attempt < 1 || !oneOf(p.Stage, "inputs", "execution", "verified") {
		return fmt.Errorf("invalid workplace profile")
	}
	if (p.Bundle != nil && p.Adapter != "") || (p.Origin.Provider == "github" && (p.Bundle != nil || p.Adapter != "")) {
		return fmt.Errorf("profile requires one acquisition mechanism")
	}
	r := finding.Report{CompletedChecks: []string{"Explicit workplace evidence acquisition; no apply or playbook execution", "Origin and source integrity checks are not signed remote attestation"}}
	o.input = profilePath
	o.changeID = p.Identity.ChangeID
	o.commit = p.Identity.Commit
	o.environment = p.Identity.Environment
	bindReportSource(&r, o, "workflow-collect", raw)
	fail := func(e error) error {
		r.IncompleteChecks = append(r.IncompleteChecks, e.Error())
		return emitReportOptions(stdout, o, r)
	}
	if p.Origin.Provider != "local" && !native {
		return fail(fmt.Errorf("remote acquisition requires explicit --native"))
	}
	var archive []byte
	if p.Origin.Provider == "github" {
		archive, e = collectGithubBundle(p)
	} else {
		if p.Origin.Provider == "spacelift" {
			if e = inspectSpaceOrigin(p); e != nil {
				return fail(e)
			}
		}
		if p.Adapter != "" {
			if !native {
				return fail(fmt.Errorf("adapter execution requires --native"))
			}
			archive, e = collectAdapterBundle(p)
		} else if p.Bundle != nil {
			b := *p.Bundle
			if !filepath.IsAbs(b.Path) {
				b.Path = filepath.Join(filepath.Dir(profilePath), b.Path)
				b.Path, _ = filepath.Abs(b.Path)
			}
			archive, e = readBoundArtifact(b)
			r.Provenance.Artifacts = append(r.Provenance.Artifacts, finding.ProvenanceArtifact{Path: b.Path, SHA256: b.SHA256})
		} else {
			e = fmt.Errorf("profile requires an export bundle or explicit adapter")
		}
		if e == nil && p.Origin.Provider == "spacelift" {
			e = inspectSpaceOrigin(p)
		}
	}
	if e != nil {
		return fail(e)
	}
	files, e := readWorkflowArchive(archive)
	if e != nil {
		return fail(e)
	}
	if e = validateExportFiles(files, p); e != nil {
		return fail(e)
	}
	absolute, e := filepath.Abs(out)
	if e != nil {
		return e
	}
	if e = os.Mkdir(absolute, 0700); e != nil {
		return fmt.Errorf("output directory must be new: %w", e)
	}
	for name, data := range files {
		target := filepath.Join(absolute, filepath.FromSlash(name))
		if e = os.MkdirAll(filepath.Dir(target), 0700); e != nil {
			return e
		}
		if e = publishMarkdown(target, data); e != nil {
			return e
		}
	}
	// Run the same checker as offline users; collection success cannot override
	// missing invocation or health evidence.
	var reviewed, diagnostics bytes.Buffer
	code := runCommand("workflow-check", []string{"--manifest", filepath.Join(absolute, "workflow.json"), "--stage", p.Stage, "--format", "json"}, strings.NewReader(""), &reviewed, &diagnostics)
	if code == 2 || reviewed.Len() == 0 {
		return fail(fmt.Errorf("collected workflow cannot be reviewed; inspect its schemas"))
	}
	report, e := decodeSessionReport(reviewed.Bytes())
	if e != nil {
		return fail(e)
	}
	if e = publishMarkdown(filepath.Join(absolute, "review.json"), reviewed.Bytes()); e != nil {
		return e
	}
	appendScanReport(&r, report)
	r.CompletedChecks = append(r.CompletedChecks, "Acquired "+p.Origin.Provider+" run "+p.Origin.RunID+"; review: "+filepath.Join(absolute, "review.json"), "Archive SHA-256: "+digestBytes(archive), "Acquired at: "+time.Now().UTC().Format(time.RFC3339Nano))
	if report.Provenance != nil {
		r.Provenance.Artifacts = append(r.Provenance.Artifacts, report.Provenance.Artifacts...)
	}
	return emitReportOptions(stdout, o, r)
}
