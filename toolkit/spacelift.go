package toolkit

import (
	"encoding/json"
	"flag"
	"fmt"
	"git-tools/finding"
	"io"
	"reflect"
	"regexp"
	"strings"
	"time"
)

type spacePolicy struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Decision string `json:"decision"`
}
type spaceApproval struct {
	Satisfied   *bool    `json:"satisfied"`
	Outstanding []string `json:"outstanding"`
}
type spaceDependency struct {
	StackID     string   `json:"stack_id"`
	RunID       string   `json:"run_id"`
	State       string   `json:"state"`
	CollectedAt string   `json:"collected_at"`
	DependsOn   []string `json:"depends_on"`
}
type spaceDrift struct {
	Enabled     *bool  `json:"enabled"`
	LastSuccess string `json:"last_success"`
	Detected    *bool  `json:"detected"`
	Reconcile   *bool  `json:"reconcile"`
}
type spacePlanBinding struct {
	JSONSHA256 string `json:"json_sha256"`
	RunID      string `json:"run_id"`
	CommitSHA  string `json:"commit_sha"`
	Source     string `json:"source"`
}
type spaceSnapshot struct {
	SchemaVersion string             `json:"schema_version"`
	Account       string             `json:"account"`
	StackID       string             `json:"stack_id"`
	RunID         string             `json:"run_id"`
	CommitSHA     string             `json:"commit_sha"`
	State         string             `json:"state"`
	RunType       string             `json:"run_type"`
	CollectedAt   string             `json:"collected_at"`
	Source        string             `json:"source"`
	LatestRunID   string             `json:"latest_run_id"`
	Policies      *[]spacePolicy     `json:"policies"`
	Approval      *spaceApproval     `json:"approval"`
	Dependencies  *[]spaceDependency `json:"dependencies"`
	Downstream    *[]string          `json:"downstream"`
	Drift         *spaceDrift        `json:"drift"`
	Config        map[string]any     `json:"config"`
	Plan          *spacePlanBinding  `json:"plan"`
}
type spaceOptions struct {
	account, stack, run, commit, runType, phase, expect, plan string
	maxAge                                                    time.Duration
}

var fullCommitPattern = regexp.MustCompile(`^[a-fA-F0-9]{40}([a-fA-F0-9]{24})?$`)

const spaceRunQuery = `query RCDORun($stack: ID!, $run: ID!) { stack(id: $stack) { id run(id: $run) { id state type commit { hash } } } }`

func collectSpace(stack, run string) (spaceSnapshot, error) {
	vars, _ := json.Marshal(map[string]string{"stack": stack, "run": run})
	data, err := collectJSON("spacectl", "api", "--raw", "--variables", string(vars), spaceRunQuery)
	if err != nil {
		return spaceSnapshot{}, fmt.Errorf("Spacelift run collection failed; verify spacectl authentication and API compatibility")
	}
	var response struct {
		Errors []json.RawMessage `json:"errors"`
		Data   struct {
			Stack *struct {
				ID  string `json:"id"`
				Run *struct {
					ID     string `json:"id"`
					State  string `json:"state"`
					Type   string `json:"type"`
					Commit struct {
						Hash string `json:"hash"`
					} `json:"commit"`
				} `json:"run"`
			} `json:"stack"`
		} `json:"data"`
	}
	if validateConfigDocument("json", data) != nil || json.Unmarshal(data, &response) != nil || len(response.Errors) > 0 || response.Data.Stack == nil || response.Data.Stack.Run == nil {
		return spaceSnapshot{}, fmt.Errorf("Spacelift API returned missing, partial or invalid run evidence")
	}
	s := response.Data.Stack
	r := s.Run
	return spaceSnapshot{SchemaVersion: "1", StackID: s.ID, RunID: r.ID, CommitSHA: r.Commit.Hash, State: r.State, RunType: r.Type, CollectedAt: time.Now().UTC().Format(time.RFC3339Nano), Source: "spacectl api: RCDORun"}, nil
}
func decodeSpace(data []byte) (spaceSnapshot, error) {
	var s spaceSnapshot
	if err := validateConfigDocument("json", data); err != nil {
		return s, err
	}
	if err := json.Unmarshal(data, &s); err != nil {
		return s, fmt.Errorf("invalid Spacelift snapshot structure")
	}
	// Only explicit top-level fields are accepted. Nested stack state is never run evidence.
	return s, nil
}
func runSpaceliftCheck(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	var collectStack, collectRun string
	var x spaceOptions
	_, o, err := parseFlags("spacelift-check", args, stderr, func(fs *flag.FlagSet) *commonOptions {
		var o commonOptions
		addCommonFlags(fs, &o)
		fs.StringVar(&collectStack, "stack", "", "collect this stack's actual run using spacectl api")
		fs.StringVar(&collectRun, "run", "", "run to collect; requires --stack")
		spaceFlags(fs, &x)
		return &o
	})
	if err != nil {
		return err
	}
	if x.commit != "" && !fullCommitPattern.MatchString(x.commit) {
		return fmt.Errorf("--expect-commit requires a full 40- or 64-digit hexadecimal commit SHA")
	}
	if x.maxAge <= 0 {
		return fmt.Errorf("--max-age must be positive")
	}
	var s spaceSnapshot
	if collectStack != "" || collectRun != "" {
		if collectStack == "" || collectRun == "" || o.input != "-" {
			return fmt.Errorf("--stack and --run are required together and cannot accompany --input")
		}
		s, err = collectSpace(collectStack, collectRun)
		if x.stack == "" {
			x.stack = collectStack
		}
		if x.run == "" {
			x.run = collectRun
		}
	} else {
		var data []byte
		data, err = readInput(o.input, stdin)
		if err == nil {
			s, err = decodeSpace(data)
		}
	}
	if err != nil {
		return emitReportOptions(stdout, o, finding.Report{IncompleteChecks: []string{"Spacelift evidence unavailable or invalid"}})
	}
	r := reviewSpace(s, x, o.environment, time.Now().UTC())
	if x.expect != "" {
		var expected map[string]any
		if err := readStrictJSONFile(x.expect, &expected); err != nil {
			return err
		}
		if len(expected) == 0 {
			return fmt.Errorf("at least one stack configuration expectation is required")
		}
		auditSpaceConfig(&r, s, expected, o.environment)
	}
	if x.plan != "" {
		checkSpaceBinding(&r, s, x.plan, o.environment)
	}
	return emitReportOptions(stdout, o, r)
}
func spaceFlags(fs *flag.FlagSet, x *spaceOptions) {
	fs.StringVar(&x.account, "expect-account", "", "expected Spacelift account identity")
	fs.StringVar(&x.stack, "expect-stack", "", "expected stack ID")
	fs.StringVar(&x.run, "expect-run", "", "expected run ID")
	fs.StringVar(&x.commit, "expect-commit", "", "expected exact commit SHA; prefixes do not match")
	fs.StringVar(&x.runType, "expect-type", "", "expected PROPOSED, TRACKED, TASK or DESTROY run")
	fs.StringVar(&x.phase, "expect-state", "", "expected exact run state")
	fs.StringVar(&x.expect, "expect-config", "", "JSON expected stack configuration (subset)")
	fs.StringVar(&x.plan, "plan-json", "", "saved-plan JSON to verify against acquisition binding")
	fs.DurationVar(&x.maxAge, "max-age", 15*time.Minute, "maximum snapshot, dependency and drift evidence age")
}
func checkFresh(r *finding.Report, label, at string, age time.Duration, now time.Time) {
	t, err := time.Parse(time.RFC3339Nano, at)
	if err != nil || t.After(now.Add(time.Minute)) || now.Sub(t) > age {
		r.IncompleteChecks = append(r.IncompleteChecks, label+": timestamp missing, stale or in the future")
	}
}
func reviewSpace(s spaceSnapshot, x spaceOptions, env string, now time.Time) finding.Report {
	r := finding.Report{Findings: []finding.Finding{}, CompletedChecks: []string{"Spacelift run review; local acknowledgement is not approval"}, IncompleteChecks: []string{}}
	if s.SchemaVersion != "1" {
		r.IncompleteChecks = append(r.IncompleteChecks, "Spacelift snapshot schema_version must be 1; legacy snapshot has incomplete coverage")
	}
	checkFresh(&r, "Run evidence", s.CollectedAt, x.maxAge, now)
	for _, v := range []struct{ name, actual, expected string }{{"account", s.Account, x.account}, {"stack", s.StackID, x.stack}, {"run", s.RunID, x.run}, {"commit", s.CommitSHA, x.commit}, {"type", s.RunType, x.runType}, {"state", s.State, x.phase}} {
		if v.actual == "" {
			r.IncompleteChecks = append(r.IncompleteChecks, "Missing run "+v.name)
		} else if v.expected != "" && v.actual != v.expected {
			addIAC(&r, "SPACE-IDENTITY", finding.SeverityCritical, "Unexpected run "+v.name, s.RunID, "verify", env, "Expected "+v.expected+"; observed "+v.actual)
		}
	}
	if s.Source == "" {
		r.IncompleteChecks = append(r.IncompleteChecks, "Missing collector source provenance")
	}
	if s.CommitSHA != "" && !fullCommitPattern.MatchString(s.CommitSHA) {
		r.IncompleteChecks = append(r.IncompleteChecks, "Run commit is not a full hexadecimal SHA")
	}
	state := strings.ToUpper(s.State)
	switch state {
	case "FINISHED":
	case "FAILED", "REJECTED", "DISCARDED", "CANCELED", "CANCELLED", "STOPPED":
		addIAC(&r, "SPACE-STATE", finding.SeverityHigh, "Run did not finish successfully", s.RunID, "review", env, "State: "+state)
	case "QUEUED", "PREPARING", "INITIALIZING", "PLANNING", "UNCONFIRMED", "APPLYING", "PERFORMING", "DESTROYING", "READY", "PENDING_REVIEW", "PENDING", "CONFIRMED", "REPLAN_REQUESTED":
		addIAC(&r, "SPACE-PENDING", finding.SeverityWarning, "Run has outstanding work", s.RunID, "review", env, "State: "+state+"; deployment success is not established")
	default:
		r.IncompleteChecks = append(r.IncompleteChecks, "Unknown or missing run state")
	}
	if !oneOf(strings.ToUpper(s.RunType), "PROPOSED", "TRACKED", "TASK", "DESTROY") {
		r.IncompleteChecks = append(r.IncompleteChecks, "Unknown run type")
	}
	if strings.EqualFold(s.RunType, "PROPOSED") {
		r.CompletedChecks = append(r.CompletedChecks, "Proposed run: completion establishes a preview, not applied infrastructure")
	}
	if s.LatestRunID == "" {
		r.IncompleteChecks = append(r.IncompleteChecks, "Latest run identity unavailable; supersession was not checked")
	} else if s.LatestRunID != s.RunID {
		addIAC(&r, "SPACE-SUPERSEDED", finding.SeverityHigh, "A newer run supersedes this snapshot", s.RunID, "verify", env, "Latest run: "+s.LatestRunID)
	}
	if s.Policies == nil {
		r.IncompleteChecks = append(r.IncompleteChecks, "Policy evidence unavailable")
	} else {
		for _, p := range *s.Policies {
			if p.ID == "" || p.Type == "" {
				r.IncompleteChecks = append(r.IncompleteChecks, "Policy identity or type unavailable")
			}
			switch strings.ToLower(p.Decision) {
			case "pass", "allow", "approve":
			case "deny", "denied", "reject", "rejected", "fail", "failed":
				addIAC(&r, "SPACE-POLICY", finding.SeverityHigh, "Spacelift policy denied the run", s.RunID, "policy", env, "Policy: "+p.ID+"; type: "+p.Type+"; decision: "+p.Decision)
			default:
				r.IncompleteChecks = append(r.IncompleteChecks, "Policy decision unknown: "+p.ID)
			}
		}
	}
	if s.Approval == nil || s.Approval.Satisfied == nil {
		r.IncompleteChecks = append(r.IncompleteChecks, "Approval requirements unavailable")
	} else if !*s.Approval.Satisfied || len(s.Approval.Outstanding) > 0 {
		addIAC(&r, "SPACE-APPROVAL", finding.SeverityHigh, "Platform approval requirements remain", s.RunID, "approval", env, redactAIText(strings.Join(s.Approval.Outstanding, "; "))+"; approval satisfied: false or outstanding requirements present")
	}
	reviewSpaceOperations(&r, s, x, env, now)
	r.CompletedChecks = append(r.CompletedChecks, "Run: "+s.RunID+"; stack: "+s.StackID+"; type: "+s.RunType+"; state: "+s.State+"; source: "+s.Source)
	return r
}
func reviewSpaceOperations(r *finding.Report, s spaceSnapshot, x spaceOptions, env string, now time.Time) {
	if s.Dependencies == nil {
		r.IncompleteChecks = append(r.IncompleteChecks, "Dependency evidence unavailable")
	} else {
		edges := map[string][]string{}
		seen := map[string]bool{}
		for _, d := range *s.Dependencies {
			if d.StackID == "" || d.RunID == "" || seen[d.StackID] {
				r.IncompleteChecks = append(r.IncompleteChecks, "Missing or duplicate dependency identity")
			}
			seen[d.StackID] = true
			edges[d.StackID] = d.DependsOn
			checkFresh(r, "Dependency "+d.StackID, d.CollectedAt, x.maxAge, now)
			if d.State != "FINISHED" {
				addIAC(r, "SPACE-DEPENDENCY", finding.SeverityHigh, "Upstream dependency is not finished", s.RunID, "dependency", env, "Stack: "+d.StackID+"; run: "+d.RunID+"; state: "+d.State)
			} else {
				r.CompletedChecks = append(r.CompletedChecks, "Upstream completed: "+d.StackID+"; run: "+d.RunID)
			}
		}
		visited, active := map[string]bool{}, map[string]bool{}
		var visit func(string) bool
		visit = func(k string) bool {
			if active[k] {
				return true
			}
			if visited[k] {
				return false
			}
			visited[k] = true
			active[k] = true
			for _, next := range edges[k] {
				if _, ok := edges[next]; !ok {
					r.IncompleteChecks = append(r.IncompleteChecks, "Dependency graph missing node: "+next)
				}
				if visit(next) {
					return true
				}
			}
			active[k] = false
			return false
		}
		for _, k := range sortedStringMapKeys(edges) {
			if visit(k) {
				addIAC(r, "SPACE-CYCLE", finding.SeverityHigh, "Dependency graph contains a cycle", s.RunID, "dependency", env, "Cycle includes "+k)
				break
			}
		}
	}
	if s.Downstream == nil {
		r.IncompleteChecks = append(r.IncompleteChecks, "Downstream impact evidence unavailable")
	} else {
		for _, d := range *s.Downstream {
			addIAC(r, "SPACE-DOWNSTREAM", finding.SeverityInfo, "Downstream stack may be affected", d, "impact", env, "Upstream stack: "+s.StackID+"; supplied dependency evidence")
		}
	}
	if s.Drift == nil || s.Drift.Enabled == nil {
		r.IncompleteChecks = append(r.IncompleteChecks, "Drift configuration unavailable")
	} else if !*s.Drift.Enabled {
		addIAC(r, "SPACE-DRIFT", finding.SeverityWarning, "Drift detection disabled", s.StackID, "drift", env, "No scheduled drift coverage reported")
	} else {
		checkFresh(r, "Last successful drift detection", s.Drift.LastSuccess, x.maxAge, now)
		if s.Drift.Detected == nil || s.Drift.Reconcile == nil {
			r.IncompleteChecks = append(r.IncompleteChecks, "Drift outcome or reconciliation configuration unavailable")
		} else if *s.Drift.Detected {
			addIAC(r, "SPACE-DRIFT", finding.SeverityWarning, "Stack drift detected", s.StackID, "drift", env, fmt.Sprintf("Reconciliation configured: %t; this does not establish that reconciliation ran", *s.Drift.Reconcile))
		}
	}
}
func sortedStringMapKeys(m map[string][]string) []string {
	a := map[string]any{}
	for k := range m {
		a[k] = true
	}
	return sortedKeys(a)
}
func auditSpaceConfig(r *finding.Report, s spaceSnapshot, expected map[string]any, env string) {
	for _, k := range sortedKeys(expected) {
		if !oneOf(k, "branch", "project_root", "engine", "engine_version", "worker_pool", "autodeploy", "policies", "contexts", "workspace", "backend", "repository", "runner_image") {
			r.IncompleteChecks = append(r.IncompleteChecks, "Unsupported stack expectation: "+k)
			continue
		}
		actual, ok := s.Config[k]
		if !ok {
			r.IncompleteChecks = append(r.IncompleteChecks, "Missing stack configuration: "+k)
		} else if !reflect.DeepEqual(actual, expected[k]) {
			addIAC(r, "SPACE-CONFIG", finding.SeverityHigh, "Stack configuration differs from expectation", s.StackID, "config", env, "Field: "+k+"; values withheld")
		} else {
			r.CompletedChecks = append(r.CompletedChecks, "Stack configuration matched: "+k)
		}
	}
}
func checkSpaceBinding(r *finding.Report, s spaceSnapshot, path, env string) {
	data, err := readConfigSource(path)
	if err != nil {
		r.IncompleteChecks = append(r.IncompleteChecks, "Bound plan cannot be read")
		return
	}
	p, err := decodePlan(data)
	if err != nil {
		r.IncompleteChecks = append(r.IncompleteChecks, "Bound plan is invalid JSON")
		return
	}
	planReport := reviewIAC(p, env, iacLimits{})
	r.Findings = append(r.Findings, planReport.Findings...)
	r.IncompleteChecks = append(r.IncompleteChecks, planReport.IncompleteChecks...)
	r.CompletedChecks = append(r.CompletedChecks, planReport.CompletedChecks...)
	if s.Plan == nil || s.Plan.Source == "" || s.Plan.JSONSHA256 == "" || s.Plan.RunID == "" || s.Plan.CommitSHA == "" {
		r.IncompleteChecks = append(r.IncompleteChecks, "Plan acquisition binding unavailable")
		return
	}
	if s.Plan.JSONSHA256 != digestBytes(data) || s.Plan.RunID != s.RunID || s.Plan.CommitSHA != s.CommitSHA {
		addIAC(r, "SPACE-BINDING", finding.SeverityCritical, "Reviewed plan does not match run binding", s.RunID, "binding", env, "Plan hash, run ID or commit differs from supplied acquisition evidence")
	} else {
		r.CompletedChecks = append(r.CompletedChecks, "Plan bytes match supplied run/commit acquisition binding; source: "+s.Plan.Source+"; this is not a signature or independent attestation")
	}
}
