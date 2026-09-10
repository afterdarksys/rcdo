package toolkit

import (
	"flag"
	"fmt"
	"git-tools/finding"
	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"io"
	"strings"
)

func runIACConfig(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	_, o, err := parseFlags("iac-config-check", args, stderr, func(fs *flag.FlagSet) *commonOptions { var o commonOptions; addCommonFlags(fs, &o); return &o })
	if err != nil {
		return err
	}
	data, err := readInput(o.input, stdin)
	if err != nil {
		return err
	}
	file, diags := hclsyntax.ParseConfig(data, o.input, hcl.Pos{Line: 1, Column: 1})
	if diags.HasErrors() {
		return fmt.Errorf("invalid HCL configuration; inspect native diagnostics locally")
	}
	r := finding.Report{CompletedChecks: []string{"Single-file HCL version, lifecycle and configuration reference review; no provider evaluation"}, Findings: []finding.Finding{}}
	body := file.Body.(*hclsyntax.Body)
	versionFound := false
	var scan func(*hclsyntax.Body, string)
	scan = func(body *hclsyntax.Body, owner string) {
		for _, name := range sortedHCLAttributes(body) {
			attr := body.Attributes[name]
			if name == "required_version" {
				versionFound = true
				value, ok := evaluateHCLValue(attr.Expr)
				if !ok {
					r.IncompleteChecks = append(r.IncompleteChecks, "Engine version constraint could not be evaluated")
				} else {
					addIAC(&r, "IAC-VERSION", finding.SeverityInfo, "Engine version constraint", owner, "config", o.environment, redactAIText(fmt.Sprint(value))+"; compatibility requires the selected engine's native validation")
				}
			}
			if oneOf(name, "prevent_destroy", "create_before_destroy", "ignore_changes") {
				value, ok := evaluateHCLValue(attr.Expr)
				evidence := "Expression requires native evaluation"
				if ok {
					evidence = fmt.Sprintf("%s: %v", name, value)
				}
				addIAC(&r, "IAC-LIFECYCLE", finding.SeverityInfo, "Lifecycle configuration", owner, "config", o.environment, evidence)
			}
			refs := map[string]bool{}
			for _, tr := range attr.Expr.Variables() {
				if len(tr) < 2 {
					continue
				}
				root := tr.RootName()
				at, ok := tr[1].(hcl.TraverseAttr)
				if !ok || oneOf(root, "var", "local", "path", "terraform", "each", "count") {
					continue
				}
				ref := root + "." + at.Name
				if root == "data" && len(tr) > 2 {
					if a, ok := tr[2].(hcl.TraverseAttr); ok {
						ref += "." + a.Name
					}
				}
				refs[ref] = true
			}
			refMap := map[string]any{}
			for k := range refs {
				refMap[k] = true
			}
			for _, ref := range sortedKeys(refMap) {
				addIAC(&r, "IAC-REFERENCE", finding.SeverityInfo, "Configuration-derived dependency", owner, "reference", o.environment, "Attribute: "+name+"; references: "+ref+"; this is not an observed live dependency")
			}
		}
		for _, b := range body.Blocks {
			child := owner + "." + b.Type
			if len(b.Labels) > 0 {
				child = b.Type + "." + strings.Join(b.Labels, ".")
			}
			if b.Type == "required_providers" {
				for _, name := range sortedHCLAttributes(b.Body) {
					value, ok := evaluateHCLValue(b.Body.Attributes[name].Expr)
					m, isMap := value.(map[string]any)
					if !ok || !isMap {
						r.IncompleteChecks = append(r.IncompleteChecks, "Provider declaration cannot be evaluated: "+name)
						continue
					}
					source, _ := m["source"].(string)
					version, _ := m["version"].(string)
					if source == "" || version == "" {
						addIAC(&r, "IAC-PROVIDER", finding.SeverityWarning, "Provider source or version constraint missing", name, "config", o.environment, "Declare source and version; review the lockfile and native initialization separately")
					} else {
						addIAC(&r, "IAC-PROVIDER", finding.SeverityInfo, "Provider requirement", name, "config", o.environment, "Source: "+source+"; version constraint: "+version)
					}
				}
			}
			if b.Type == "module" {
				source := b.Body.Attributes["source"]
				if source == nil {
					r.IncompleteChecks = append(r.IncompleteChecks, "Module source missing: "+child)
				} else {
					v, ok := evaluateHCLValue(source.Expr)
					s, isString := v.(string)
					if !ok || !isString {
						r.IncompleteChecks = append(r.IncompleteChecks, "Module source is not literal: "+child)
					} else if !strings.HasPrefix(s, "./") && !strings.HasPrefix(s, "../") && b.Body.Attributes["version"] == nil && !strings.Contains(s, "?ref=") {
						addIAC(&r, "IAC-MODULE", finding.SeverityWarning, "Remote module has no version or revision pin", child, "config", o.environment, "Review module source and pinning before initialization")
					}
				}
			}
			scan(b.Body, child)
		}
	}
	scan(body, "configuration")
	if !versionFound {
		addIAC(&r, "IAC-VERSION", finding.SeverityWarning, "No engine version constraint in this file", "configuration", "config", o.environment, "Check other module files for required_version; single-file absence does not prove module-wide absence")
	}
	return emitReportOptions(stdout, o, r)
}
func sortedHCLAttributes(body *hclsyntax.Body) []string {
	m := map[string]any{}
	for k := range body.Attributes {
		m[k] = true
	}
	return sortedKeys(m)
}
