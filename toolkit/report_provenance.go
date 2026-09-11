package toolkit

import (
	"fmt"
	"git-tools/finding"
	"strings"
	"time"
)

// Capture inputs before a checker reads them and recheck them at emission. This
// binds secondary policy/expectation files as well as the primary source.
func prepareReportProvenance(name string, args []string, o *commonOptions) error {
	if o.changeID == "" && o.commit == "" {
		return nil
	}
	p := &finding.Provenance{SchemaVersion: "1", Tool: strings.Fields(name)[0], ChangeID: o.changeID, Commit: o.commit, Environment: o.environment, SourceSHA256: strings.Repeat("0", 64), CollectedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	if o.changeID == "" || o.commit == "" || p.Validate() != nil {
		return fmt.Errorf("provenance requires --change-id, full --commit and environment")
	}
	fileFlags := map[string]bool{"input": true, "before": true, "after": true, "expect": true, "limits": true, "policy": true, "rego": true, "repo-policy": true, "expect-config": true, "manifest": true, "plan": true, "baseline": true, "suite": true, "config": true}
	// Commands whose main evidence comes from live state cannot substitute an
	// unrelated --input file for the observation. Their collector must bind it.
	unsupported := map[string]bool{"review": true, "git-review": true, "network-check": true, "doctor": true, "collect": true, "workflow-collect": true}
	primary := ""
	primaryFlags := map[string]bool{"input": true, "before": true, "after": true, "manifest": true, "plan": true, "suite": true}
	seen := map[string]bool{}
	for _, arg := range args {
		if oneOf(arg, "--native", "--native=true", "-native", "-native=true", "--collect", "--collect=true", "-collect", "-collect=true") && oneOf(p.Tool, "iac-validate", "kube-explain", "cloud-context-check") {
			unsupported[p.Tool] = true
		}
	}
	for i := 0; i < len(args); i++ {
		key, value, has := strings.Cut(strings.TrimLeft(args[i], "-"), "=")
		if !strings.HasPrefix(args[i], "-") || !fileFlags[key] {
			continue
		}
		if !has && i+1 < len(args) {
			i++
			value = args[i]
		}
		if value == "" || value == "-" {
			continue
		}
		a, _, err := captureArtifact(value)
		if err != nil {
			o.bindingGaps = append(o.bindingGaps, "Provenance input unavailable: --"+key)
			continue
		}
		if !seen[a.Path] {
			p.Artifacts = append(p.Artifacts, finding.ProvenanceArtifact{Path: a.Path, SHA256: a.SHA256})
			seen[a.Path] = true
		}
		if primaryFlags[key] && (primary == "" || key == "input") {
			primary = a.SHA256
		}
	}
	if primary != "" && !unsupported[p.Tool] {
		p.SourceSHA256 = primary
		o.provenance = p
	}
	return nil
}
