package toolkit

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	ctyjson "github.com/zclconf/go-cty/cty/json"
	"gopkg.in/yaml.v3"
)

type decompositionPlan struct {
	SchemaVersion string              `json:"schema_version"`
	SourceFormat  string              `json:"source_format"`
	TargetCLI     string              `json:"target_cli"`
	Region        string              `json:"region,omitempty"`
	Profile       string              `json:"profile,omitempty"`
	Status        string              `json:"status"`
	TotalSteps    int                 `json:"total_steps"`
	SelectedStep  int                 `json:"selected_step,omitempty"`
	Steps         []decompositionStep `json:"steps"`
	Unresolved    []string            `json:"unresolved"`
}

type decompositionStep struct {
	Number         int            `json:"number"`
	ID             string         `json:"id"`
	Source         string         `json:"source"`
	Action         string         `json:"action"`
	DependsOn      []string       `json:"depends_on"`
	Command        string         `json:"command"`
	RequestExample map[string]any `json:"request_example"`
	Capture        []string       `json:"capture"`
	Verify         string         `json:"verify"`
	Rollback       string         `json:"rollback"`
	Notes          []string       `json:"notes"`
}

type decompositionOptions struct {
	input   string
	from    string
	to      string
	format  string
	step    int
	width   int
	region  string
	profile string
}

type sourceResource struct {
	Address      string
	Kind         string
	Name         string
	DisplayName  string
	Attributes   map[string]any
	Dependencies []string
	Unresolved   []string
	Order        int
}

type recipeArgument struct {
	Source    string
	Parameter string
	Flag      string
	Required  bool
	Default   any
}

type resourceRecipe struct {
	Kind           string
	Service        string
	Operation      string
	Arguments      []recipeArgument
	CaptureField   string
	Verify         string
	Rollback       string
	Description    string
	RequiresRegion bool
}

var regionNamePattern = regexp.MustCompile(`^[A-Za-z0-9-]+$`)
var profileNamePattern = regexp.MustCompile(`^[A-Za-z0-9_.@-]+$`)

func runDecompose(command string, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	options := decompositionOptions{input: "-", from: "auto", to: "aws", format: "text", width: 100}
	switch command {
	case "hcl2aws":
		options.from, options.to = "hcl", "aws"
	case "hcl2ali":
		options.from, options.to = "hcl", "alicloud"
	case "ansible2aws":
		options.from, options.to = "ansible", "aws"
	case "ansible2ali":
		options.from, options.to = "ansible", "alicloud"
	}

	fs := flag.NewFlagSet(command, flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&options.input, "input", options.input, "IaC file; use - for standard input")
	if command == "decompose" {
		fs.StringVar(&options.from, "from", options.from, "source syntax: auto, hcl, or ansible")
		fs.StringVar(&options.to, "to", options.to, "target CLI: aws or alicloud")
	}
	fs.StringVar(&options.format, "format", options.format, "output format: text or json")
	fs.IntVar(&options.step, "step", 0, "show one numbered step; 0 shows the complete plan")
	fs.IntVar(&options.width, "width", options.width, "maximum explanatory text width; minimum 40")
	fs.StringVar(&options.region, "region", "", "cloud region to include in generated commands")
	fs.StringVar(&options.profile, "profile", "", "CLI profile to include in generated commands")
	setAccessibleUsage(fs, command, stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected arguments: %v", fs.Args())
	}
	if !oneOf(options.from, "auto", "hcl", "ansible") {
		return fmt.Errorf("--from must be auto, hcl, or ansible")
	}
	if !oneOf(options.to, "aws", "alicloud") {
		return fmt.Errorf("--to must be aws or alicloud")
	}
	if !oneOf(options.format, "text", "json") {
		return fmt.Errorf("--format must be text or json")
	}
	if options.step < 0 {
		return fmt.Errorf("--step cannot be negative")
	}
	if options.width < 40 {
		return fmt.Errorf("--width must be at least 40")
	}
	if options.region != "" && !regionNamePattern.MatchString(options.region) {
		return fmt.Errorf("--region contains unsupported characters")
	}
	if options.profile != "" && !profileNamePattern.MatchString(options.profile) {
		return fmt.Errorf("--profile contains unsupported characters")
	}
	data, err := readInput(options.input, stdin)
	if err != nil {
		return err
	}
	resolved := options.from
	if resolved == "auto" {
		resolved = detectDecompositionSource(options.input, data)
	}
	var resources []sourceResource
	var unresolved []string
	if resolved == "hcl" {
		resources, unresolved, err = parseHCLResources(options.input, data)
	} else {
		resources, unresolved, err = parseAnsibleResources(data, options.to)
	}
	if err != nil {
		return err
	}
	plan := buildDecompositionPlan(resolved, options.to, options.region, options.profile, resources, unresolved)
	if options.step > len(plan.Steps) {
		return fmt.Errorf("--step %d does not exist; plan contains %d steps", options.step, len(plan.Steps))
	}
	if options.step > 0 {
		plan.SelectedStep = options.step
		plan.Steps = []decompositionStep{plan.Steps[options.step-1]}
	}
	if options.format == "json" {
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(plan); err != nil {
			return err
		}
	} else {
		renderDecompositionText(stdout, plan, options.width)
	}
	if plan.Status == "incomplete" {
		return reportError{status: "incomplete"}
	}
	return nil
}

func detectDecompositionSource(input string, data []byte) string {
	extension := strings.ToLower(filepath.Ext(input))
	if oneOf(extension, ".tf", ".hcl", ".tfvars", ".tofu") || hclHint.Match(data) {
		return "hcl"
	}
	return "ansible"
}

var hclResourceReference = regexp.MustCompile(`\b((?:aws|alicloud)_[A-Za-z0-9_]+\.[A-Za-z0-9_-]+)(?:\.[A-Za-z0-9_]+)?`)
var hclDynamicReference = regexp.MustCompile(`\b(var|local|module|data|each|count)\.[A-Za-z0-9_.-]+`)
var hclPureResourceReference = regexp.MustCompile(`^"?\$?\{?((?:aws|alicloud)_[A-Za-z0-9_]+\.[A-Za-z0-9_-]+)\.id\}?"?$`)

func parseHCLResources(input string, data []byte) ([]sourceResource, []string, error) {
	name := input
	if name == "-" {
		name = "standard-input.tf"
	}
	file, diagnostics := hclsyntax.ParseConfig(data, name, hcl.Pos{Line: 1, Column: 1})
	if diagnostics.HasErrors() {
		return nil, nil, fmt.Errorf("parse HCL: %s", diagnostics.Error())
	}
	body := file.Body.(*hclsyntax.Body)
	var resources []sourceResource
	var unresolved []string
	for _, block := range body.Blocks {
		if block.Type != "resource" {
			if oneOf(block.Type, "module", "data", "dynamic") {
				unresolved = append(unresolved, fmt.Sprintf("%s block at line %d requires evaluated Terraform/OpenTofu context", block.Type, block.TypeRange.Start.Line))
			}
			continue
		}
		if len(block.Labels) != 2 {
			unresolved = append(unresolved, fmt.Sprintf("resource block at line %d does not have type and name labels", block.TypeRange.Start.Line))
			continue
		}
		resource := sourceResource{
			Address: block.Labels[0] + "." + block.Labels[1], Kind: block.Labels[0], Name: block.Labels[1],
			DisplayName: block.Labels[0] + "." + block.Labels[1], Attributes: map[string]any{}, Order: len(resources),
		}
		dependencySet := map[string]bool{}
		attributeNames := make([]string, 0, len(block.Body.Attributes))
		for attributeName := range block.Body.Attributes {
			attributeNames = append(attributeNames, attributeName)
		}
		sort.Strings(attributeNames)
		for _, attributeName := range attributeNames {
			attribute := block.Body.Attributes[attributeName]
			raw := hclExpressionSource(attribute.Expr.Range(), data)
			value, known := evaluateHCLValue(attribute.Expr)
			if !known {
				value = translateHCLExpression(raw)
				if attributeName != "depends_on" && strings.HasPrefix(fmt.Sprint(value), "<expression:") {
					resource.Unresolved = append(resource.Unresolved, fmt.Sprintf("%s uses an expression requiring evaluated state: %s", attributeName, compactExpression(raw)))
				}
			}
			if isSensitivePath(attributeName) {
				value = "[REDACTED]"
				resource.Unresolved = append(resource.Unresolved, attributeName+" was redacted and must be supplied securely")
			}
			resource.Attributes[attributeName] = value
			for _, match := range hclResourceReference.FindAllStringSubmatch(raw, -1) {
				if match[1] != resource.Address {
					dependencySet[match[1]] = true
				}
			}
		}
		for _, nested := range block.Body.Blocks {
			resource.Unresolved = append(resource.Unresolved, fmt.Sprintf("nested %s block at line %d requires manual translation", nested.Type, nested.TypeRange.Start.Line))
		}
		for dependency := range dependencySet {
			resource.Dependencies = append(resource.Dependencies, dependency)
		}
		sort.Strings(resource.Dependencies)
		resources = append(resources, resource)
	}
	if len(resources) == 0 {
		return nil, nil, fmt.Errorf("HCL contains no top-level resource blocks")
	}
	return resources, unresolved, nil
}

func hclExpressionSource(expressionRange hcl.Range, source []byte) string {
	start, end := expressionRange.Start.Byte, expressionRange.End.Byte
	if start < 0 || end < start || end > len(source) {
		return "<unavailable-expression>"
	}
	return strings.TrimSpace(string(source[start:end]))
}

func evaluateHCLValue(expression hclsyntax.Expression) (any, bool) {
	value, diagnostics := expression.Value(nil)
	if diagnostics.HasErrors() || !value.IsKnown() || value.IsNull() {
		return nil, false
	}
	data, err := ctyjson.Marshal(value, value.Type())
	if err != nil {
		return nil, false
	}
	var result any
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.UseNumber()
	if err := decoder.Decode(&result); err != nil {
		return nil, false
	}
	return result, true
}

func translateHCLExpression(value string) string {
	trimmed := strings.TrimSpace(value)
	if match := hclPureResourceReference.FindStringSubmatch(trimmed); len(match) == 2 && !hclDynamicReference.MatchString(trimmed) {
		return "$" + captureVariable(match[1])
	}
	return "<expression: " + compactExpression(value) + ">"
}

func compactExpression(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

func parseAnsibleResources(data []byte, target string) ([]sourceResource, []string, error) {
	var raw any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, nil, fmt.Errorf("parse Ansible YAML: %w", err)
	}
	raw = normalizeConfigMaps(raw)
	plays, ok := raw.([]any)
	if !ok {
		return nil, nil, fmt.Errorf("Ansible input must be a YAML sequence of plays")
	}
	var resources []sourceResource
	var unresolved []string
	for playIndex, playValue := range plays {
		play, ok := playValue.(map[string]any)
		if !ok {
			unresolved = append(unresolved, fmt.Sprintf("play %d is not a mapping", playIndex+1))
			continue
		}
		tasks, _ := play["tasks"].([]any)
		for taskIndex, taskValue := range tasks {
			task, ok := taskValue.(map[string]any)
			if !ok {
				unresolved = append(unresolved, fmt.Sprintf("play %d task %d is not a mapping", playIndex+1, taskIndex+1))
				continue
			}
			module, arguments := ansibleModule(task)
			if module == "" {
				unresolved = append(unresolved, fmt.Sprintf("play %d task %d has no recognized module action", playIndex+1, taskIndex+1))
				continue
			}
			kind := ansibleModuleKind(module, target)
			name, _ := task["name"].(string)
			if name == "" {
				name = module
			}
			resource := sourceResource{
				Address: fmt.Sprintf("ansible.play%d.task%d", playIndex+1, taskIndex+1), Kind: kind,
				Name: fmt.Sprintf("task%d", taskIndex+1), DisplayName: name, Attributes: arguments, Order: len(resources),
			}
			if len(resources) > 0 {
				resource.Dependencies = []string{resources[len(resources)-1].Address}
			}
			for key, value := range arguments {
				if strings.Contains(fmt.Sprint(value), "{{") {
					resource.Attributes[key] = "<ansible: " + compactExpression(fmt.Sprint(value)) + ">"
					resource.Unresolved = append(resource.Unresolved, key+" contains a Jinja expression")
				}
				if isSensitivePath(key) {
					resource.Attributes[key] = "[REDACTED]"
					resource.Unresolved = append(resource.Unresolved, key+" was redacted and must be supplied securely")
				}
			}
			for _, control := range []string{"when", "loop", "with_items", "until", "delegate_to"} {
				if _, exists := task[control]; exists {
					resource.Unresolved = append(resource.Unresolved, "Ansible "+control+" behavior requires manual evaluation")
				}
			}
			if state, ok := arguments["state"].(string); ok && oneOf(strings.ToLower(state), "absent", "deleted") {
				resource.Kind = "unsupported-delete:" + module
				resource.Unresolved = append(resource.Unresolved, "deletion task was not converted into a create command")
			}
			resources = append(resources, resource)
		}
	}
	if len(resources) == 0 {
		return nil, unresolved, fmt.Errorf("Ansible input contains no tasks")
	}
	return resources, unresolved, nil
}

var ansibleMetadata = map[string]bool{
	"name": true, "when": true, "register": true, "tags": true, "vars": true, "become": true,
	"loop": true, "with_items": true, "until": true, "retries": true, "delay": true,
	"delegate_to": true, "changed_when": true, "failed_when": true, "check_mode": true,
}

func ansibleModule(task map[string]any) (string, map[string]any) {
	keys := make([]string, 0, len(task))
	for key := range task {
		if !ansibleMetadata[key] {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	if len(keys) != 1 {
		return "", nil
	}
	module := keys[0]
	arguments, ok := task[module].(map[string]any)
	if !ok {
		return module, map[string]any{}
	}
	return module, arguments
}

func ansibleModuleKind(module, target string) string {
	short := module
	if index := strings.LastIndex(module, "."); index >= 0 {
		short = module[index+1:]
	}
	if target == "aws" {
		switch short {
		case "ec2_vpc_net":
			return "aws_vpc"
		case "ec2_vpc_subnet":
			return "aws_subnet"
		case "ec2_group":
			return "aws_security_group"
		case "ec2_instance":
			return "aws_instance"
		case "s3_bucket":
			return "aws_s3_bucket"
		case "iam_role":
			return "aws_iam_role"
		}
	} else {
		switch short {
		case "alicloud_vpc", "ali_vpc":
			return "alicloud_vpc"
		case "alicloud_vswitch", "ali_vswitch":
			return "alicloud_vswitch"
		case "alicloud_security_group", "ali_security_group":
			return "alicloud_security_group"
		case "alicloud_instance", "ali_instance":
			return "alicloud_instance"
		case "alicloud_oss_bucket", "ali_oss_bucket":
			return "alicloud_oss_bucket"
		case "alicloud_ram_role", "ali_ram_role":
			return "alicloud_ram_role"
		}
	}
	return "unsupported:" + module
}

func buildDecompositionPlan(source, target, region, profile string, resources []sourceResource, unresolved []string) decompositionPlan {
	ordered, orderingIssues := orderSourceResources(resources)
	unresolved = append(unresolved, orderingIssues...)
	plan := decompositionPlan{SchemaVersion: "1", SourceFormat: source, TargetCLI: target, Region: region, Profile: profile, Status: "ready", Steps: []decompositionStep{}, Unresolved: []string{}}
	known := map[string]bool{}
	for _, resource := range resources {
		known[resource.Address] = true
	}
	for _, resource := range ordered {
		resource = normalizeRecipeAttributes(resource)
		unresolved = append(unresolved, resource.Unresolved...)
		recipe, ok := decompositionRecipe(target, resource.Kind)
		if !ok {
			unresolved = append(unresolved, resource.Address+" uses unsupported resource/module type "+resource.Kind)
			continue
		}
		step, issues := recipeStep(target, region, profile, resource, recipe)
		for _, dependency := range resource.Dependencies {
			if known[dependency] {
				step.DependsOn = append(step.DependsOn, dependency)
			} else {
				issues = append(issues, resource.Address+" depends on unavailable "+dependency)
			}
		}
		step.Number = len(plan.Steps) + 1
		plan.Steps = append(plan.Steps, step)
		unresolved = append(unresolved, issues...)
	}
	plan.Unresolved = uniqueStrings(unresolved)
	if len(plan.Unresolved) > 0 {
		plan.Status = "incomplete"
	}
	plan.TotalSteps = len(plan.Steps)
	return plan
}

func orderSourceResources(resources []sourceResource) ([]sourceResource, []string) {
	byAddress := map[string]sourceResource{}
	for _, resource := range resources {
		byAddress[resource.Address] = resource
	}
	visited, visiting := map[string]bool{}, map[string]bool{}
	var ordered []sourceResource
	var issues []string
	var visit func(sourceResource)
	visit = func(resource sourceResource) {
		if visited[resource.Address] {
			return
		}
		if visiting[resource.Address] {
			issues = append(issues, "dependency cycle includes "+resource.Address)
			return
		}
		visiting[resource.Address] = true
		for _, dependency := range resource.Dependencies {
			if found, ok := byAddress[dependency]; ok {
				visit(found)
			}
		}
		visiting[resource.Address] = false
		visited[resource.Address] = true
		ordered = append(ordered, resource)
	}
	sorted := append([]sourceResource(nil), resources...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Order < sorted[j].Order })
	for _, resource := range sorted {
		visit(resource)
	}
	return ordered, issues
}

func uniqueStrings(values []string) []string {
	seen := map[string]bool{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		if strings.TrimSpace(value) != "" && !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	return result
}

func recipeStep(target, region, profile string, resource sourceResource, recipe resourceRecipe) (decompositionStep, []string) {
	step := decompositionStep{
		ID: resource.Address, Source: resource.DisplayName, Action: recipe.Description,
		DependsOn: []string{}, RequestExample: map[string]any{}, Capture: []string{}, Notes: []string{},
	}
	commandParts := []string{}
	if target == "aws" {
		commandParts = append(commandParts, "aws", recipe.Service, recipe.Operation)
	} else {
		commandParts = append(commandParts, "aliyun", recipe.Service, recipe.Operation)
	}
	var issues []string
	used := map[string]bool{}
	for _, argument := range recipe.Arguments {
		value, exists := resource.Attributes[argument.Source]
		if (!exists || emptyDecompositionValue(value)) && argument.Default != nil {
			value, exists = argument.Default, true
		}
		if !exists || emptyDecompositionValue(value) {
			if argument.Required {
				placeholder := "<required:" + argument.Source + ">"
				value = placeholder
				issues = append(issues, resource.Address+" requires "+argument.Source)
			} else {
				continue
			}
		}
		used[argument.Source] = true
		step.RequestExample[argument.Parameter] = value
		commandParts = append(commandParts, argument.Flag)
		commandParts = append(commandParts, shellValues(value)...)
		if decompositionPlaceholder(value) {
			issues = append(issues, resource.Address+" contains a placeholder for "+argument.Source)
		}
	}
	if target == "aws" {
		if resource.Kind == "aws_s3_bucket" && region != "" && region != "us-east-1" {
			configuration := "LocationConstraint=" + region
			step.RequestExample["CreateBucketConfiguration"] = map[string]any{"LocationConstraint": region}
			commandParts = append(commandParts, "--create-bucket-configuration", shellQuote(configuration))
		}
		if region != "" {
			commandParts = append(commandParts, "--region", shellQuote(region))
		}
		if profile != "" {
			commandParts = append(commandParts, "--profile", shellQuote(profile))
		}
	} else {
		if recipe.RequiresRegion {
			if region == "" {
				commandParts = append(commandParts, "--RegionId", shellQuote("<required:region>"))
				step.RequestExample["RegionId"] = "<required:region>"
				issues = append(issues, resource.Address+" requires an explicit AliCloud region")
			} else {
				commandParts = append(commandParts, "--RegionId", shellQuote(region))
				step.RequestExample["RegionId"] = region
			}
		}
		if profile != "" {
			commandParts = append(commandParts, "--profile", shellQuote(profile))
		}
	}
	for attribute := range resource.Attributes {
		if !used[attribute] && !oneOf(attribute, "depends_on", "provider", "lifecycle", "state") && !recipeAliasAttribute(resource.Kind, attribute) {
			note := "Source attribute " + attribute + " is not mapped to this CLI example."
			step.Notes = append(step.Notes, note)
			issues = append(issues, resource.Address+" has unmapped source attribute "+attribute)
		}
	}
	sort.Strings(step.Notes)
	variable := captureVariable(resource.Address)
	step.Command = wrapCLICommand(commandParts)
	if recipe.CaptureField != "" {
		if strings.Contains(recipe.CaptureField, "used in request") {
			step.Capture = append(step.Capture, fmt.Sprintf("Set %s to %s.", variable, recipe.CaptureField))
		} else {
			step.Capture = append(step.Capture, fmt.Sprintf("Capture %s from the response as %s.", recipe.CaptureField, variable))
		}
	}
	step.Verify = addDecompositionCLIContext(strings.ReplaceAll(recipe.Verify, "$ID", "$"+variable), target, region, profile, recipe.RequiresRegion)
	step.Rollback = addDecompositionCLIContext(strings.ReplaceAll(recipe.Rollback, "$ID", "$"+variable), target, region, profile, recipe.RequiresRegion)
	if resource.Kind == "aws_s3_bucket" && region == "" {
		step.Notes = append(step.Notes, "Bucket creation outside us-east-1 may require a region-specific CreateBucketConfiguration.")
		issues = append(issues, resource.Address+" requires review of region-specific bucket creation parameters")
	}
	return step, issues
}

func addDecompositionCLIContext(command, target, region, profile string, requiresRegion bool) string {
	command = strings.ReplaceAll(command, " --", " \\\n  --")
	if target == "aws" {
		if region != "" {
			command += " \\\n  --region " + shellQuote(region)
		}
		if profile != "" {
			command += " \\\n  --profile " + shellQuote(profile)
		}
		return command
	}
	if requiresRegion && region != "" {
		command += " \\\n  --RegionId " + shellQuote(region)
	}
	if profile != "" {
		command += " \\\n  --profile " + shellQuote(profile)
	}
	return command
}

func decompositionPlaceholder(value any) bool {
	switch typed := value.(type) {
	case string:
		upper := strings.ToUpper(typed)
		return strings.Contains(upper, "REPLACE_ME") || strings.Contains(upper, "CHANGEME") || strings.HasPrefix(typed, "<required:") || strings.HasPrefix(typed, "<expression:") || strings.HasPrefix(typed, "<ansible:")
	case []any:
		for _, item := range typed {
			if decompositionPlaceholder(item) {
				return true
			}
		}
	case map[string]any:
		for _, item := range typed {
			if decompositionPlaceholder(item) {
				return true
			}
		}
	}
	return false
}

func recipeAliasAttribute(kind, attribute string) bool {
	switch kind + ":" + attribute {
	case "aws_vpc:name", "aws_vpc:tags", "aws_instance:ami", "aws_s3_bucket:name", "aws_iam_role:assume_role_policy", "alicloud_vpc:vpc_name", "alicloud_security_group:security_group_name", "alicloud_instance:security_groups":
		return true
	default:
		return false
	}
}

func emptyDecompositionValue(value any) bool {
	if value == nil {
		return true
	}
	if text, ok := value.(string); ok {
		return strings.TrimSpace(text) == ""
	}
	return false
}

func shellValues(value any) []string {
	switch typed := value.(type) {
	case []any:
		for _, item := range typed {
			switch item.(type) {
			case map[string]any, []any:
				data, _ := json.Marshal(typed)
				return []string{shellQuote(string(data))}
			}
		}
		values := make([]string, 0, len(typed))
		for _, item := range typed {
			values = append(values, shellQuote(fmt.Sprint(item)))
		}
		return values
	case map[string]any:
		data, _ := json.Marshal(typed)
		return []string{shellQuote(string(data))}
	case bool:
		return []string{strconv.FormatBool(typed)}
	default:
		return []string{shellQuote(fmt.Sprint(value))}
	}
}

var captureVariablePattern = regexp.MustCompile(`^\$RCDO_[A-Za-z0-9_]+$`)

func shellQuote(value string) string {
	if captureVariablePattern.MatchString(value) {
		return `"` + value + `"`
	}
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}

func wrapCLICommand(parts []string) string {
	if len(parts) <= 3 {
		return strings.Join(parts, " ")
	}
	var result strings.Builder
	result.WriteString(strings.Join(parts[:3], " "))
	for index := 3; index < len(parts); index += 2 {
		result.WriteString(" \\")
		result.WriteString("\n  ")
		result.WriteString(parts[index])
		if index+1 < len(parts) {
			result.WriteString(" ")
			result.WriteString(parts[index+1])
		}
	}
	return result.String()
}

func normalizeRecipeAttributes(resource sourceResource) sourceResource {
	copyValue := func(target string, sources ...string) {
		if _, exists := resource.Attributes[target]; exists {
			return
		}
		for _, source := range sources {
			if value, exists := resource.Attributes[source]; exists {
				resource.Attributes[target] = value
				return
			}
		}
	}
	switch resource.Kind {
	case "aws_vpc":
		tags := map[string]any{}
		if configured, ok := resource.Attributes["tags"].(map[string]any); ok {
			for key, value := range configured {
				tags[key] = value
			}
		}
		if name, exists := resource.Attributes["name"]; exists {
			tags["Name"] = name
		}
		if len(tags) > 0 {
			keys := make([]string, 0, len(tags))
			for key := range tags {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			items := make([]any, 0, len(keys))
			for _, key := range keys {
				items = append(items, map[string]any{"Key": key, "Value": tags[key]})
			}
			resource.Attributes["tag_specifications"] = []any{map[string]any{"ResourceType": "vpc", "Tags": items}}
		}
	case "aws_instance":
		copyValue("image_id", "ami")
	case "aws_s3_bucket":
		copyValue("bucket", "name")
	case "aws_iam_role":
		copyValue("assume_role_policy_document", "assume_role_policy")
	case "alicloud_vpc":
		copyValue("name", "vpc_name")
	case "alicloud_security_group":
		copyValue("name", "security_group_name")
	case "alicloud_instance":
		if groups, ok := resource.Attributes["security_groups"].([]any); ok {
			if len(groups) == 1 {
				resource.Attributes["security_group_id"] = groups[0]
			} else if len(groups) > 1 {
				resource.Unresolved = append(resource.Unresolved, "multiple security_groups require indexed AliCloud CLI parameters")
			}
		}
	}
	if oneOf(resource.Kind, "aws_security_group", "alicloud_vpc", "alicloud_security_group", "aws_iam_role", "alicloud_ram_role") {
		if _, exists := resource.Attributes["name"]; !exists {
			resource.Attributes["name"] = resource.Name
			resource.Unresolved = append(resource.Unresolved, "name was not explicit; the source block/task label is used as an example")
		}
	}
	return resource
}

var nonIdentifier = regexp.MustCompile(`[^A-Za-z0-9]+`)

func captureVariable(address string) string {
	return "RCDO_" + strings.ToUpper(strings.Trim(nonIdentifier.ReplaceAllString(address, "_"), "_")) + "_ID"
}

func renderDecompositionText(w io.Writer, plan decompositionPlan, width int) {
	fmt.Fprintln(w, "IAC DECOMPOSITION PLAN")
	fmt.Fprintf(w, "Status: %s\nSource: %s\nTarget CLI: %s\n", strings.ToUpper(plan.Status), plan.SourceFormat, plan.TargetCLI)
	if plan.Region != "" {
		fmt.Fprintf(w, "Region: %s\n", plan.Region)
	}
	if plan.Profile != "" {
		fmt.Fprintf(w, "Profile: %s\n", plan.Profile)
	}
	fmt.Fprintf(w, "Steps in plan: %d\n", plan.TotalSteps)
	if plan.SelectedStep > 0 {
		fmt.Fprintf(w, "Selected step: %d\n", plan.SelectedStep)
	}
	fmt.Fprintln(w, "Execution: NOT RUN")
	for _, step := range plan.Steps {
		fmt.Fprintf(w, "\nSTEP %d\n", step.Number)
		writeWrapped(w, "ID: "+step.ID, width)
		writeWrapped(w, "Source: "+step.Source, width)
		writeWrapped(w, "Action: "+step.Action, width)
		if len(step.DependsOn) > 0 {
			writeWrapped(w, "Depends on: "+strings.Join(step.DependsOn, ", "), width)
		} else {
			fmt.Fprintln(w, "Depends on: none")
		}
		fmt.Fprintln(w, "Command example:")
		fmt.Fprintln(w, step.Command)
		fmt.Fprintln(w, "Request example:")
		request, _ := json.MarshalIndent(step.RequestExample, "", "  ")
		fmt.Fprintln(w, string(request))
		for _, capture := range step.Capture {
			writeWrapped(w, "Capture: "+capture, width)
		}
		fmt.Fprintln(w, "Verify command:")
		fmt.Fprintln(w, step.Verify)
		fmt.Fprintln(w, "Rollback command:")
		fmt.Fprintln(w, step.Rollback)
		for _, note := range step.Notes {
			writeWrapped(w, "Note: "+note, width)
		}
	}
	if len(plan.Unresolved) > 0 {
		fmt.Fprintln(w, "\nUNRESOLVED ITEMS")
		for index, item := range plan.Unresolved {
			writeWrapped(w, fmt.Sprintf("%d. %s", index+1, item), width)
		}
	}
	fmt.Fprintln(w, "\nNEXT")
	if plan.SelectedStep > 0 && plan.SelectedStep < plan.TotalSteps {
		fmt.Fprintf(w, "Continue with --step %d.\n", plan.SelectedStep+1)
	} else if plan.SelectedStep == 0 && plan.TotalSteps > 1 {
		fmt.Fprintln(w, "Inspect an individual operation with --step NUMBER.")
	}
	writeWrapped(w, "Review placeholders and request examples against current provider documentation before running anything manually.", width)
}

func decompositionRecipe(target, kind string) (resourceRecipe, bool) {
	recipes := awsDecompositionRecipes()
	if target == "alicloud" {
		recipes = alicloudDecompositionRecipes()
	}
	recipe, ok := recipes[kind]
	return recipe, ok
}

func awsDecompositionRecipes() map[string]resourceRecipe {
	return map[string]resourceRecipe{
		"aws_vpc": {
			Kind: "aws_vpc", Service: "ec2", Operation: "create-vpc", Description: "Create a VPC",
			Arguments:    []recipeArgument{{"cidr_block", "CidrBlock", "--cidr-block", true, nil}, {"instance_tenancy", "InstanceTenancy", "--instance-tenancy", false, nil}, {"tag_specifications", "TagSpecifications", "--tag-specifications", false, nil}},
			CaptureField: "Vpc.VpcId", Verify: `aws ec2 describe-vpcs --vpc-ids "$ID"`, Rollback: `aws ec2 delete-vpc --vpc-id "$ID"`,
		},
		"aws_subnet": {
			Kind: "aws_subnet", Service: "ec2", Operation: "create-subnet", Description: "Create a subnet",
			Arguments:    []recipeArgument{{"vpc_id", "VpcId", "--vpc-id", true, nil}, {"cidr_block", "CidrBlock", "--cidr-block", true, nil}, {"availability_zone", "AvailabilityZone", "--availability-zone", false, nil}},
			CaptureField: "Subnet.SubnetId", Verify: `aws ec2 describe-subnets --subnet-ids "$ID"`, Rollback: `aws ec2 delete-subnet --subnet-id "$ID"`,
		},
		"aws_security_group": {
			Kind: "aws_security_group", Service: "ec2", Operation: "create-security-group", Description: "Create a security group",
			Arguments:    []recipeArgument{{"name", "GroupName", "--group-name", true, nil}, {"description", "Description", "--description", true, nil}, {"vpc_id", "VpcId", "--vpc-id", true, nil}},
			CaptureField: "GroupId", Verify: `aws ec2 describe-security-groups --group-ids "$ID"`, Rollback: `aws ec2 delete-security-group --group-id "$ID"`,
		},
		"aws_instance": {
			Kind: "aws_instance", Service: "ec2", Operation: "run-instances", Description: "Launch an EC2 instance",
			Arguments:    []recipeArgument{{"image_id", "ImageId", "--image-id", true, nil}, {"instance_type", "InstanceType", "--instance-type", true, nil}, {"subnet_id", "SubnetId", "--subnet-id", false, nil}, {"security_group_ids", "SecurityGroupIds", "--security-group-ids", false, nil}, {"key_name", "KeyName", "--key-name", false, nil}},
			CaptureField: "Instances[0].InstanceId", Verify: `aws ec2 describe-instances --instance-ids "$ID"`, Rollback: `aws ec2 terminate-instances --instance-ids "$ID"`,
		},
		"aws_s3_bucket": {
			Kind: "aws_s3_bucket", Service: "s3api", Operation: "create-bucket", Description: "Create an S3 bucket",
			Arguments: []recipeArgument{{"bucket", "Bucket", "--bucket", true, nil}}, CaptureField: "the bucket name used in request",
			Verify: `aws s3api head-bucket --bucket "$ID"`, Rollback: `aws s3api delete-bucket --bucket "$ID"`,
		},
		"aws_iam_role": {
			Kind: "aws_iam_role", Service: "iam", Operation: "create-role", Description: "Create an IAM role",
			Arguments:    []recipeArgument{{"name", "RoleName", "--role-name", true, nil}, {"assume_role_policy_document", "AssumeRolePolicyDocument", "--assume-role-policy-document", true, nil}},
			CaptureField: "Role.RoleName", Verify: `aws iam get-role --role-name "$ID"`, Rollback: `aws iam delete-role --role-name "$ID"`,
		},
	}
}

func alicloudDecompositionRecipes() map[string]resourceRecipe {
	return map[string]resourceRecipe{
		"alicloud_vpc": {
			Kind: "alicloud_vpc", Service: "vpc", Operation: "CreateVpc", Description: "Create an AliCloud VPC",
			Arguments:    []recipeArgument{{"name", "VpcName", "--VpcName", false, nil}, {"cidr_block", "CidrBlock", "--CidrBlock", true, nil}},
			CaptureField: "VpcId", Verify: `aliyun vpc DescribeVpcs --VpcId "$ID"`, Rollback: `aliyun vpc DeleteVpc --VpcId "$ID"`, RequiresRegion: true,
		},
		"alicloud_vswitch": {
			Kind: "alicloud_vswitch", Service: "vpc", Operation: "CreateVSwitch", Description: "Create an AliCloud VSwitch",
			Arguments:    []recipeArgument{{"vpc_id", "VpcId", "--VpcId", true, nil}, {"cidr_block", "CidrBlock", "--CidrBlock", true, nil}, {"zone_id", "ZoneId", "--ZoneId", true, nil}, {"vswitch_name", "VSwitchName", "--VSwitchName", false, nil}},
			CaptureField: "VSwitchId", Verify: `aliyun vpc DescribeVSwitches --VSwitchId "$ID"`, Rollback: `aliyun vpc DeleteVSwitch --VSwitchId "$ID"`, RequiresRegion: true,
		},
		"alicloud_security_group": {
			Kind: "alicloud_security_group", Service: "ecs", Operation: "CreateSecurityGroup", Description: "Create an AliCloud security group",
			Arguments:    []recipeArgument{{"name", "SecurityGroupName", "--SecurityGroupName", false, nil}, {"vpc_id", "VpcId", "--VpcId", true, nil}, {"description", "Description", "--Description", false, nil}},
			CaptureField: "SecurityGroupId", Verify: `aliyun ecs DescribeSecurityGroups --SecurityGroupId "$ID"`, Rollback: `aliyun ecs DeleteSecurityGroup --SecurityGroupId "$ID"`, RequiresRegion: true,
		},
		"alicloud_instance": {
			Kind: "alicloud_instance", Service: "ecs", Operation: "RunInstances", Description: "Launch an AliCloud ECS instance",
			Arguments:    []recipeArgument{{"image_id", "ImageId", "--ImageId", true, nil}, {"instance_type", "InstanceType", "--InstanceType", true, nil}, {"vswitch_id", "VSwitchId", "--VSwitchId", true, nil}, {"security_group_id", "SecurityGroupId", "--SecurityGroupId", true, nil}, {"instance_name", "InstanceName", "--InstanceName", false, nil}},
			CaptureField: "InstanceIdSets.InstanceIdSet[0]", Verify: `aliyun ecs DescribeInstances --InstanceIds "[\"$ID\"]"`, Rollback: `aliyun ecs DeleteInstance --InstanceId "$ID" --Force true`, RequiresRegion: true,
		},
		"alicloud_ram_role": {
			Kind: "alicloud_ram_role", Service: "ram", Operation: "CreateRole", Description: "Create an AliCloud RAM role",
			Arguments:    []recipeArgument{{"name", "RoleName", "--RoleName", true, nil}, {"document", "AssumeRolePolicyDocument", "--AssumeRolePolicyDocument", true, nil}, {"description", "Description", "--Description", false, nil}},
			CaptureField: "Role.RoleName", Verify: `aliyun ram GetRole --RoleName "$ID"`, Rollback: `aliyun ram DeleteRole --RoleName "$ID"`,
		},
	}
}
