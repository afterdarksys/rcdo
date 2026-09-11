package toolkit

import (
	"encoding/json"
	"flag"
	"fmt"
	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"gopkg.in/yaml.v3"
	"io"
	"path/filepath"
	"strings"
)

type commandRecipe struct {
	Shell          string             `json:"shell,omitempty"`
	Effect         string             `json:"effect,omitempty"`
	Parameters     []commandParameter `json:"parameters,omitempty"`
	RequiredInputs []string           `json:"required_inputs,omitempty"`
	SchemaVersion  string             `json:"schema_version"`
	Target         string             `json:"target"`
	Action         string             `json:"action"`
	SourceSHA256   string             `json:"source_sha256"`
	Executed       bool               `json:"executed"`
	Mutating       bool               `json:"mutating"`
	Argv           []string           `json:"argv"`
	Command        string             `json:"command"`
	Notes          []string           `json:"notes"`
}

func literalShellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'" }
func runCommandGen(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	var input, target, action, from, format, region, profile, directory string
	var width, step int
	var explain bool
	var shell string
	fs := flag.NewFlagSet("command-gen", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.BoolVar(&explain, "explain", false, "explain parameters, required inputs and effects")
	fs.StringVar(&shell, "shell", "posix", "posix or powershell (PowerShell 7.3+ Standard native argument passing)")
	fs.StringVar(&input, "input", "-", "configuration file or - for stdin")
	fs.StringVar(&target, "to", "", "spacelift, aws, alicloud, gcp, tofu or terraform")
	fs.StringVar(&action, "action", "", "Spacelift: show, logs, changes, preview, deploy; IaC: validate, plan, fmt-check; cloud: create")
	fs.StringVar(&from, "from", "auto", "cloud source: auto, hcl or ansible")
	fs.StringVar(&format, "format", "text", "text or json")
	fs.StringVar(&region, "region", "", "cloud region")
	fs.StringVar(&profile, "profile", "", "cloud profile")
	fs.StringVar(&directory, "directory", "", "IaC module directory; defaults to source directory")
	fs.IntVar(&width, "width", 100, "text width; minimum 40")
	fs.IntVar(&step, "step", 0, "cloud decomposition step")
	setAccessibleUsage(fs, "command-gen", stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 || width < 40 || !oneOf(format, "text", "json") || !oneOf(shell, "posix", "powershell") {
		return fmt.Errorf("invalid arguments, width or format")
	}
	if target == "gcp" {
		if directory != "" || profile != "" || region != "" || step != 0 || from != "auto" || !oneOf(action, "", "create") {
			return fmt.Errorf("GCP create previews require a complete JSON/YAML request; put project/account/location in that request")
		}
		return runGCPCommandGen(input, format, shell, width, stdin, stdout)
	}
	if oneOf(target, "aws", "alicloud") {
		if action != "" && action != "create" {
			return fmt.Errorf("cloud generation supports --action create; verification and rollback examples are included")
		}
		if directory != "" {
			return fmt.Errorf("directory is only valid for IaC commands")
		}
		if explain || shell != "posix" {
			return runExplainedCloud(input, target, from, format, region, profile, shell, step, width, stdin, stdout)
		}
		return runDecompose("decompose", []string{"--input", input, "--from", from, "--to", target, "--format", format, "--region", region, "--profile", profile, "--width", fmt.Sprint(width), "--step", fmt.Sprint(step)}, stdin, stdout, stderr)
	}
	if !oneOf(target, "spacelift", "tofu", "terraform") {
		return fmt.Errorf("--to must be spacelift, aws, alicloud, gcp, tofu or terraform")
	}
	if region != "" || profile != "" || step != 0 || from != "auto" {
		return fmt.Errorf("--region, --profile, --step and --from are cloud-only options")
	}
	data, err := readInput(input, stdin)
	if err != nil {
		return err
	}
	recipe := commandRecipe{SchemaVersion: "1", Target: target, SourceSHA256: digestBytes(data), Notes: []string{"Generated command only; nothing was executed. Review target identity before running."}}
	if target == "spacelift" {
		if directory != "" {
			return fmt.Errorf("--directory is only supported for Terraform/OpenTofu")
		}
		var config struct {
			StackID   string `json:"stack_id" yaml:"stack_id"`
			RunID     string `json:"run_id" yaml:"run_id"`
			CommitSHA string `json:"commit_sha" yaml:"commit_sha"`
		}
		syntax, err := detectConfigSyntax("auto", input, data)
		if err != nil {
			return err
		}
		switch syntax {
		case "json":
			if err := strictJSON(data, &config); err != nil {
				return err
			}
		case "yaml":
			if err := validateConfigDocument("yaml", data); err != nil {
				return err
			}
			decoder := yaml.NewDecoder(strings.NewReader(string(data)))
			decoder.KnownFields(true)
			if decoder.Decode(&config) != nil {
				return fmt.Errorf("invalid Spacelift command configuration")
			}
		case "hcl":
			// Use explicit configuration for an existing stack. Names of new stack resources
			// cannot establish their eventual platform IDs.
			file, diags := hclsyntax.ParseConfig(data, input, hcl.Pos{Line: 1, Column: 1})
			if diags.HasErrors() {
				return fmt.Errorf("invalid HCL command configuration")
			}
			body := file.Body.(*hclsyntax.Body)
			if len(body.Blocks) > 0 {
				return fmt.Errorf("Spacelift command HCL requires top-level stack_id, run_id and commit_sha attributes; resource names are not platform IDs")
			}
			for name, attr := range body.Attributes {
				v, ok := evaluateHCLValue(attr.Expr)
				s, isString := v.(string)
				if !ok || !isString {
					return fmt.Errorf("command attributes must be literal strings")
				}
				switch name {
				case "stack_id":
					config.StackID = s
				case "run_id":
					config.RunID = s
				case "commit_sha":
					config.CommitSHA = s
				default:
					return fmt.Errorf("unsupported command attribute")
				}
			}
		default:
			return fmt.Errorf("Spacelift command configuration must be JSON, YAML or HCL")
		}
		if !operationLabel(config.StackID) || strings.HasPrefix(config.StackID, "-") {
			return fmt.Errorf("stack_id is required; RCDO does not infer a live ID from a display name")
		}
		if action == "" {
			action = "show"
		}
		switch action {
		case "show":
			recipe.Argv = []string{"spacectl", "stack", "show", "--id", config.StackID, "--output", "json", "--no-color"}
			recipe.Notes = append(recipe.Notes, "Shows stack configuration; this is not an actual run snapshot.")
		case "logs", "changes":
			if !operationLabel(config.RunID) || strings.HasPrefix(config.RunID, "-") {
				return fmt.Errorf("run_id is required for %s", action)
			}
			recipe.Argv = []string{"spacectl", "stack", action, "--id", config.StackID, "--run", config.RunID}
		case "preview", "deploy":
			if !fullCommitPattern.MatchString(config.CommitSHA) {
				return fmt.Errorf("a full hexadecimal commit_sha is required for %s", action)
			}
			recipe.Argv = []string{"spacectl", "stack", action, "--id", config.StackID, "--sha", config.CommitSHA}
			recipe.Mutating = true
			recipe.Notes = append(recipe.Notes, "Running this command creates a remote run. Stack policies and auto-deploy settings determine subsequent execution.")
		default:
			return fmt.Errorf("unsupported Spacelift action; use show, logs, changes, preview or deploy")
		}
	} else {
		file, diags := hclsyntax.ParseConfig(data, input, hcl.Pos{Line: 1, Column: 1})
		if diags.HasErrors() || file == nil {
			return fmt.Errorf("Terraform/OpenTofu command generation requires valid HCL configuration")
		}
		if directory == "" {
			if input == "-" {
				return fmt.Errorf("--directory is required with stdin")
			}
			directory = filepath.Dir(input)
		}
		directory, err = filepath.Abs(directory)
		if err != nil {
			return err
		}
		if action == "" {
			action = "validate"
		}
		recipe.Argv = []string{target, "-chdir=" + directory}
		switch action {
		case "validate":
			recipe.Argv = append(recipe.Argv, "validate", "-json")
		case "fmt-check":
			recipe.Argv = append(recipe.Argv, "fmt", "-check", "-diff")
		case "plan":
			recipe.Argv = append(recipe.Argv, "plan", "-input=false", "-out=review.tfplan")
			recipe.Mutating = true
			recipe.Notes = append(recipe.Notes, "Planning reads providers/backends, may acquire a state lock, and writes review.tfplan. Review that saved plan before applying.")
		default:
			return fmt.Errorf("unsupported IaC action; use validate, plan or fmt-check")
		}
		recipe.Notes = append(recipe.Notes, "The command operates on the entire module directory. Providers/modules must already be initialized; variables and backend identity are not inferred from one file.")
	}
	recipe.Action = action
	for _, arg := range recipe.Argv {
		if strings.ContainsAny(arg, "\x00\n\r") {
			return fmt.Errorf("command arguments cannot contain NUL or line breaks")
		}
	}
	recipe.Command, err = quoteCommand(recipe.Argv, shell)
	if err != nil {
		return err
	}
	if explain || shell != "posix" {
		explainRecipe(&recipe, shell)
	}

	if format == "json" {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(recipe)
	}
	if explain || shell != "posix" {
		renderExplainedRecipe(stdout, recipe, width)
		return nil
	}
	fmt.Fprintln(stdout, "EXECUTION: NOT RUN")
	writeWrapped(stdout, fmt.Sprintf("Target: %s. Action: %s. Side effects if executed: %t.", target, action, recipe.Mutating), width)
	fmt.Fprintln(stdout, "Command: "+recipe.Command)
	for _, note := range recipe.Notes {
		writeWrapped(stdout, "Note: "+note, width)
	}
	return nil
}
