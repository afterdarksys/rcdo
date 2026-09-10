package toolkit

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"unicode"
)

type commandParameter struct {
	Flag        string `json:"flag"`
	Source      string `json:"source"`
	Required    bool   `json:"required"`
	Explanation string `json:"explanation"`
}

func quoteCommand(argv []string, shell string) (string, error) {
	if !oneOf(shell, "posix", "powershell") {
		return "", fmt.Errorf("shell must be posix or powershell")
	}
	if len(argv) == 0 {
		return "", fmt.Errorf("command has no arguments")
	}
	quoted := make([]string, len(argv))
	for i, arg := range argv {
		if strings.ContainsFunc(arg, func(r rune) bool { return unicode.IsControl(r) || unicode.Is(unicode.Cf, r) }) {
			return "", fmt.Errorf("command arguments contain control or format characters")
		}
		if shell == "powershell" {
			if strings.ContainsAny(arg, "‘’“”") {
				return "", fmt.Errorf("PowerShell smart quote arguments are unsupported; use exact JSON argv")
			}
			quoted[i] = "'" + strings.ReplaceAll(arg, "'", "''") + "'"
		} else {
			quoted[i] = literalShellQuote(arg)
		}
	}
	prefix := ""
	if shell == "powershell" {
		prefix = "& "
	}
	return prefix + strings.Join(quoted, " "), nil
}
func rawCommandValues(value any) []string {
	switch v := value.(type) {
	case []any:
		for _, item := range v {
			switch item.(type) {
			case map[string]any, []any:
				b, _ := json.Marshal(v)
				return []string{string(b)}
			}
		}
		result := []string{}
		for _, item := range v {
			result = append(result, fmt.Sprint(item))
		}
		return result
	case map[string]any:
		b, _ := json.Marshal(v)
		return []string{string(b)}
	default:
		return []string{fmt.Sprint(value)}
	}
}
func parameterMeaning(flag string) string {
	meanings := map[string]string{"--id": "Selects the exact Spacelift stack ID", "--run": "Selects a specific run rather than the latest run", "--sha": "Binds the requested run to a full commit SHA", "--output": "Selects structured CLI output", "--no-color": "Disables ANSI color output", "-json": "Requests machine-readable output; sensitive content may still be present", "-check": "Checks formatting without rewriting files", "-diff": "Displays formatting differences", "-input=false": "Disables interactive variable input", "-out=review.tfplan": "Writes a saved plan file for later review", "--region": "Selects the AWS region; does not establish account identity", "--profile": "Selects the CLI credential profile", "--RegionId": "Selects the AliCloud API region", "--cidr-block": "Sets the requested network address range", "--vpc-id": "Selects the parent VPC by exact ID", "--subnet-id": "Selects the subnet by exact ID", "--image-id": "Selects the machine image", "--instance-type": "Sets the requested compute instance size", "--bucket": "Sets the bucket name", "--group-name": "Sets the security group name", "--description": "Sets the resource description", "--security-group-ids": "Attaches the listed security groups", "--key-name": "Selects the existing SSH key pair name", "--assume-role-policy-document": "Defines who may assume the new role", "--role-name": "Sets the role name", "--tag-specifications": "Assigns resource tags", "--create-bucket-configuration": "Sets region-specific bucket creation parameters"}
	if strings.HasPrefix(flag, "-chdir=") {
		return "Runs the IaC command in this entire module directory"
	}
	if value, ok := meanings[flag]; ok {
		return value
	}
	return "Maps the named configuration field to this provider CLI parameter; verify provider-specific constraints"
}
func explainRecipe(recipe *commandRecipe, shell string) {
	recipe.Shell = shell
	recipe.RequiredInputs = []string{}
	recipe.Parameters = []commandParameter{}
	recipe.Effect = "read-only"
	if recipe.Mutating {
		recipe.Effect = "remote-write"
		if oneOf(recipe.Target, "tofu", "terraform") {
			recipe.Effect = "local-plan-write-and-backend-lock"
		}
	}
	for _, arg := range recipe.Argv {
		if strings.HasPrefix(arg, "-") {
			recipe.Parameters = append(recipe.Parameters, commandParameter{Flag: arg, Source: "configuration or selected action", Required: true, Explanation: parameterMeaning(arg)})
		}
	}
	if shell == "powershell" {
		recipe.Notes = append(recipe.Notes, "Requires PowerShell 7.3+ with Standard native argument passing; Windows PowerShell/Legacy mode is not supported.")
	}
	recipe.Notes = append(recipe.Notes, "Read/write classification describes intended CLI operations, not a guarantee about executable or provider-plugin behavior.")
}
func renderExplainedRecipe(w io.Writer, r commandRecipe, width int) {
	writeWrapped(w, "Execution: NOT RUN. Effect: "+r.Effect+". Shell: "+r.Shell, width)
	writeWrapped(w, "Target: "+r.Target+". Action: "+r.Action, width)
	if r.Command != "" {
		fmt.Fprintln(w, "Command: "+r.Command)
	} else {
		writeWrapped(w, "Command withheld until required inputs and unmapped configuration are resolved.", width)
	}
	for _, p := range r.Parameters {
		writeWrapped(w, fmt.Sprintf("Parameter %s. Source: %s. Required: %t. %s", p.Flag, p.Source, p.Required, p.Explanation), width)
	}
	for _, missing := range r.RequiredInputs {
		writeWrapped(w, "Required review/input: "+safeReportText(missing), width)
	}
	for _, note := range r.Notes {
		writeWrapped(w, "Note: "+safeReportText(note), width)
	}
}
func runExplainedCloud(input, target, from, format, region, profile, shell string, step, width int, stdin io.Reader, stdout io.Writer) error {
	if !oneOf(from, "auto", "hcl", "ansible") || step < 0 || (region != "" && !regionNamePattern.MatchString(region)) || (profile != "" && !profileNamePattern.MatchString(profile)) {
		return fmt.Errorf("invalid cloud generation options")
	}
	var data []byte
	var err error
	if input == "-" {
		data, err = io.ReadAll(io.LimitReader(stdin, 16<<20+1))
	} else {
		data, err = readConfigSource(input)
	}
	if err != nil {
		return err
	}
	if len(data) > 16<<20 {
		return fmt.Errorf("configuration exceeds 16 MiB")
	}
	if from == "auto" {
		from = detectDecompositionSource(input, data)
	}
	var resources []sourceResource
	var unresolved []string
	if from == "hcl" {
		resources, unresolved, err = parseHCLResources(input, data)
	} else {
		resources, unresolved, err = parseAnsibleResources(data, target)
	}
	if err != nil {
		return err
	}
	plan := buildDecompositionPlan(from, target, region, profile, resources, unresolved)
	if step > len(plan.Steps) {
		return fmt.Errorf("requested step does not exist")
	}
	recipes := []commandRecipe{}
	incomplete := len(plan.Unresolved) > 0 || len(plan.Steps) == 0
	for _, s := range plan.Steps {
		if step > 0 && s.Number != step {
			continue
		}
		r := commandRecipe{SchemaVersion: "1", Target: target, Action: s.Action, SourceSHA256: digestBytes(data), Mutating: true, Argv: s.Argv, Notes: append([]string{"Generated from " + s.ID + ". No command was executed."}, s.Notes...)}
		explainRecipe(&r, shell)
		r.Parameters = s.Parameters
		r.RequiredInputs = append(r.RequiredInputs, plan.Unresolved...)
		if region == "" {
			r.RequiredInputs = append(r.RequiredInputs, "Supply an explicit region before generating a runnable cloud command")
		}
		for _, arg := range r.Argv {
			if strings.Contains(arg, "$RCDO_") || strings.Contains(arg, "[REDACTED]") || decompositionPlaceholder(arg) {
				r.RequiredInputs = append(r.RequiredInputs, "Resolve captured IDs, redacted values or placeholders in the source configuration")
			}
		}
		if len(r.RequiredInputs) > 0 {
			r.Argv = nil
			incomplete = true
		} else {
			r.Command, err = quoteCommand(r.Argv, shell)
			if err != nil {
				return err
			}
		}
		recipes = append(recipes, r)
	}
	if format == "json" {
		err = json.NewEncoder(stdout).Encode(struct {
			SchemaVersion string          `json:"schema_version"`
			Recipes       []commandRecipe `json:"recipes"`
			Unresolved    []string        `json:"unresolved"`
		}{"1", recipes, plan.Unresolved})
	} else {
		for i, r := range recipes {
			fmt.Fprintf(stdout, "Step %d of %d\n", i+1, len(recipes))
			renderExplainedRecipe(stdout, r, width)
		}
		if len(recipes) == 0 {
			writeWrapped(stdout, "No supported command recipes were generated.", width)
		}
	}
	if err != nil {
		return err
	}
	if incomplete {
		return reportError{status: "incomplete"}
	}
	return nil
}
