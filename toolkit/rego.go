package toolkit

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"git-tools/finding"
)

// Deliberately allow only pure builtins, including on newer OPA releases.
const regoBuiltins = `eq assign internal.member_2 internal.member_3 internal.template_string abs ceil floor round plus minus mul div rem numbers.range count sum product max min sort all any concat contains startswith endswith lower upper split replace replace_n trim trim_left trim_right trim_prefix trim_suffix trim_space substring indexof sprintf format_int to_number is_number is_string is_boolean is_array is_object is_set is_null type_name equal neq gt gte lt lte and or minus union intersection walk object.get object.keys object.filter object.remove object.subset object.union object.union_n array.concat array.reverse array.slice json.marshal json.unmarshal json.is_valid regex.match regex.is_valid glob.match semver.compare semver.is_valid net.cidr_contains net.cidr_intersects net.cidr_is_valid strings.any_prefix_match strings.any_suffix_match`

var regoQueryPattern = regexp.MustCompile(`^data(?:\.[A-Za-z_][A-Za-z0-9_]*)+$`)

var regoInvoke = func(ctx context.Context, name string, args []string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.WaitDelay = time.Second
	// No inherited cloud credentials, OPA configuration, or home directory.
	cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + os.TempDir()}
	out := limitedCommandBuffer{limit: 16 << 20}
	diagnostics := limitedCommandBuffer{limit: 64 << 10}
	cmd.Stdout, cmd.Stderr = &out, &diagnostics
	err := cmd.Run()
	if ctx.Err() != nil {
		return nil, fmt.Errorf("OPA exceeded evaluation timeout")
	}
	if out.exceeded || diagnostics.exceeded {
		return nil, fmt.Errorf("OPA output exceeded limit")
	}
	// OPA diagnostics can contain literals from policy or input. Never echo them.
	if err != nil {
		return nil, fmt.Errorf("OPA failed; check policy syntax, permitted builtins, and decision query")
	}
	return out.Bytes(), nil
}

func runRego(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	var options commonOptions
	var modules []string
	var query string
	var timeout time.Duration
	_, options, err := parseFlags("rego-check", args, stderr, func(fs *flag.FlagSet) *commonOptions {
		fs.StringVar(&options.input, "input", "-", "JSON input file or - for stdin")
		fs.StringVar(&options.format, "format", "text", "text, json, sarif, or github")
		fs.IntVar(&options.width, "width", finding.DefaultTextWidth, "text width")
		fs.StringVar(&options.environment, "environment", "unknown", "environment label")
		fs.StringVar(&query, "query", "data.rcdo.decision", "decision reference (data.package.rule)")
		fs.DurationVar(&timeout, "timeout", 5*time.Second, "total OPA timeout, 100ms to 1m")
		fs.Func("rego", "Rego module file; repeat for multiple modules", func(v string) error { modules = append(modules, v); return nil })
		return &options
	})
	if err != nil {
		return err
	}
	if len(modules) == 0 || len(modules) > 32 {
		return fmt.Errorf("provide 1 to 32 --rego files")
	}
	if !regoQueryPattern.MatchString(query) {
		return fmt.Errorf("query must be a data.package.rule reference")
	}
	if timeout < 100*time.Millisecond || timeout > time.Minute {
		return fmt.Errorf("timeout must be between 100ms and 1m")
	}
	var input []byte
	if options.input == "-" {
		input, err = io.ReadAll(io.LimitReader(stdin, (16<<20)+1))
	} else {
		input, err = readConfigSource(options.input)
	}
	if err != nil {
		return err
	}
	if len(input) > 16<<20 {
		return fmt.Errorf("input exceeds 16 MiB")
	}
	if err = validateConfigDocument("json", input); err != nil {
		return err
	}
	dir, err := os.MkdirTemp("", "rcdo-rego-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	report := finding.Report{CompletedChecks: []string{fmt.Sprintf("Input SHA-256: %x", sha256.Sum256(input))}}
	evalArgs := []string{"eval", "--format=json", "--strict", "--strict-builtin-errors", "--timeout=" + timeout.String(), "--input", filepath.Join(dir, "input.json"), "--capabilities", filepath.Join(dir, "capabilities.json")}
	if err = os.WriteFile(filepath.Join(dir, "input.json"), input, 0600); err != nil {
		return err
	}
	total := 0
	for i, path := range modules {
		data, e := readConfigSource(path)
		if e != nil {
			return fmt.Errorf("cannot read Rego module %d: %w", i+1, e)
		}
		total += len(data)
		if total > 16<<20 {
			return fmt.Errorf("combined Rego modules exceed 16 MiB")
		}
		target := filepath.Join(dir, fmt.Sprintf("module%d.rego", i))
		if err = os.WriteFile(target, data, 0600); err != nil {
			return err
		}
		evalArgs = append(evalArgs, "--data", target)
		report.CompletedChecks = append(report.CompletedChecks, fmt.Sprintf("Module %d SHA-256: %x", i+1, sha256.Sum256(data)))
	}
	incomplete := func(message string) error {
		report.IncompleteChecks = append(report.IncompleteChecks, message)
		return emitReportOptions(stdout, options, report)
	}
	opa, err := exec.LookPath("opa")
	if err != nil {
		return incomplete("OPA executable unavailable; install OPA 1.x and place opa on PATH")
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	raw, err := regoInvoke(ctx, opa, []string{"capabilities", "--current"})
	if err != nil {
		return incomplete(err.Error())
	}
	var caps map[string]json.RawMessage
	if json.Unmarshal(raw, &caps) != nil {
		return incomplete("OPA returned invalid capabilities")
	}
	var builtins []struct {
		Name string          `json:"name"`
		Decl json.RawMessage `json:"decl"`
	}
	if json.Unmarshal(caps["builtins"], &builtins) != nil {
		return incomplete("OPA returned invalid builtin capabilities")
	}
	allowed := map[string]bool{}
	for _, name := range strings.Fields(regoBuiltins) {
		allowed[name] = true
	}
	filtered := builtins[:0]
	for _, b := range builtins {
		if allowed[b.Name] {
			filtered = append(filtered, b)
		}
	}
	caps["builtins"], err = json.Marshal(filtered)
	if err != nil {
		return err
	}
	caps["allow_net"] = json.RawMessage(`[]`)
	raw, err = json.Marshal(caps)
	if err != nil {
		return err
	}
	if err = os.WriteFile(filepath.Join(dir, "capabilities.json"), raw, 0600); err != nil {
		return err
	}
	raw, err = regoInvoke(ctx, opa, append(evalArgs, query))
	if err != nil {
		return incomplete(err.Error())
	}
	var result struct {
		Result []struct {
			Expressions []struct {
				Value json.RawMessage `json:"value"`
			} `json:"expressions"`
		} `json:"result"`
		Errors json.RawMessage `json:"errors"`
	}
	if json.Unmarshal(raw, &result) != nil || len(result.Errors) > 0 || len(result.Result) != 1 || len(result.Result[0].Expressions) != 1 {
		return incomplete("OPA decision is undefined or evaluation response is invalid")
	}
	decisionReport, err := regoDecision(result.Result[0].Expressions[0].Value, options.environment)
	if err != nil {
		return incomplete(err.Error())
	}
	report.Findings = decisionReport.Findings
	report.IncompleteChecks = decisionReport.IncompleteChecks
	report.CompletedChecks = append(report.CompletedChecks, "Evaluated "+query+" with restricted local builtins")
	return emitReportOptions(stdout, options, report)
}

func regoDecision(raw []byte, environment string) (finding.Report, error) {
	var decision struct {
		Allow    *bool `json:"allow"`
		Findings []struct {
			ID          string           `json:"id"`
			Severity    finding.Severity `json:"severity"`
			Title       string           `json:"title"`
			Resource    string           `json:"resource"`
			Reason      string           `json:"reason"`
			Remediation string           `json:"remediation"`
		} `json:"findings"`
		Incomplete []string `json:"incomplete"`
	}
	report := finding.Report{}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&decision); err != nil || decision.Allow == nil {
		return report, fmt.Errorf("decision must be an object with boolean allow and optional findings/incomplete arrays")
	}
	report.IncompleteChecks = decision.Incomplete
	for _, f := range decision.Findings {
		report.Findings = append(report.Findings, makeFinding("rego."+f.ID, f.Severity, f.Title, f.Resource, "evaluate", environment, f.Reason, "Rego policy decision", f.Remediation))
		if strings.TrimSpace(f.ID) == "" {
			return finding.Report{}, fmt.Errorf("Rego finding requires an id")
		}
	}
	if !*decision.Allow {
		report.Findings = append(report.Findings, makeFinding("rego-denied", finding.SeverityHigh, "Rego policy denied the input", "input", "evaluate", environment, "Decision allow is false", "Explicit policy decision", "Resolve policy findings before proceeding"))
	}
	if err := report.Validate(); err != nil {
		return finding.Report{}, fmt.Errorf("invalid Rego decision findings; check required fields, severity, unique IDs, and incomplete messages")
	}
	return report, nil
}
