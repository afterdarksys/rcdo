package toolkit

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"sort"
	"strings"

	"git-tools/finding"
)

// prebuild reads a saved Terraform or OpenTofu plan and an Ansible playbook.
// It lists what the plan would create, change, or delete, and what the
// playbook would ask Ansible to do. It does not run terraform, tofu, or ansible.

type prebuildRow struct {
	Action  string `json:"action"`
	Class   string `json:"class"`
	System  string `json:"system"`
	Address string `json:"address"`
	Detail  string `json:"detail"`
}

func runPrebuild(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	var planPath, playbookPath, colorMode string
	var overrides stringList
	_, options, err := parseFlags("prebuild", args, stderr, func(fs *flag.FlagSet) *commonOptions {
		var options commonOptions
		addCommonFlags(fs, &options)
		fs.StringVar(&planPath, "plan", "", "terraform show -json or tofu show -json file")
		fs.StringVar(&playbookPath, "playbook", "", "Ansible playbook YAML")
		fs.StringVar(&colorMode, "color", "auto", "auto, always, or never; auto colors a terminal only")
		fs.Var(&overrides, "color-flag", "class=color override; repeatable. Underscore form --color_flag is also accepted")
		fs.Var(&overrides, "color_flag", "same as --color-flag")
		return &options
	})
	if err != nil {
		return err
	}
	if !oneOf(options.format, "text", "json") || !oneOf(colorMode, "auto", "always", "never") || (planPath == "" && playbookPath == "") {
		return fmt.Errorf("prebuild requires --plan, --playbook, or both, and --color auto, always, or never")
	}
	palette := defaultColorPalette()
	if err := applyColorFlags(palette, overrides); err != nil {
		return err
	}
	rows := []prebuildRow{}
	gaps := []string{}
	if planPath == "" {
		gaps = append(gaps, "Terraform or OpenTofu plan was not supplied. This prebuild does not cover both systems.")
	} else {
		data, err := readInput(planPath, stdin)
		if err != nil {
			return err
		}
		plan, err := decodePlan(data)
		if err != nil {
			return err
		}
		if plan.Errored || (plan.Complete != nil && !*plan.Complete) || len(plan.DeferredChanges) > 0 {
			gaps = append(gaps, "Plan is errored, incomplete, or has deferred changes")
		}
		rows = append(rows, planRows(plan)...)
	}
	if playbookPath == "" {
		gaps = append(gaps, "Ansible playbook was not supplied. This prebuild does not cover both systems.")
	} else {
		data, err := readConfigSource(playbookPath)
		if err != nil {
			return err
		}
		if len(data) > 1<<20 {
			return fmt.Errorf("playbook exceeds 1 MiB")
		}
		plays, err := readAnsiblePlays(data)
		if err != nil {
			return err
		}
		extra, err := playbookRows(plays)
		if err != nil {
			return err
		}
		rows = append(rows, extra...)
		ensure := false
		for _, row := range extra {
			if row.Action == "ensure" {
				ensure = true
			}
		}
		if ensure {
			gaps = append(gaps, "Ansible ensure tasks were not compared with the host, so create and update are not distinguished.")
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Action != rows[j].Action {
			return rows[i].Action < rows[j].Action
		}
		if rows[i].System != rows[j].System {
			return rows[i].System < rows[j].System
		}
		return rows[i].Address < rows[j].Address
	})
	counts := map[string]int{}
	unknown := false
	for _, row := range rows {
		counts[row.Action]++
		if row.Action == "unknown" {
			unknown = true
		}
	}
	if unknown {
		gaps = append(gaps, "A plan action was not one of create, update, delete, replace, read, or no-op")
	}
	status := finding.StatusClean
	if len(gaps) > 0 {
		status = finding.StatusIncomplete
	}
	for _, row := range rows {
		if oneOf(row.Action, "delete", "replace", "update", "create", "ensure", "task", "drift") {
			if status == finding.StatusClean {
				status = finding.StatusReview
			}
		}
	}
	if options.format == "json" {
		payload := struct {
			Schema  string         `json:"schema"`
			Applied bool           `json:"applied"`
			Status  string         `json:"status"`
			Counts  map[string]int `json:"counts"`
			Gaps    []string       `json:"gaps"`
			Rows    []prebuildRow  `json:"rows"`
		}{Schema: "rcdo/prebuild/v1", Applied: false, Status: string(status), Counts: counts, Gaps: gaps, Rows: rows}
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		encoder.SetEscapeHTML(false)
		if err := encoder.Encode(payload); err != nil {
			return err
		}
	} else {
		enabled := colorEnabled(colorMode, stdout)
		var output strings.Builder
		writeWrapped(&output, "PREBUILD", options.width)
		writeWrapped(&output, "Nothing was applied. This reads a saved plan and a playbook.", options.width)
		writeWrapped(&output, fmt.Sprintf("Creates: %d. Updates: %d. Deletes: %d. Replaces: %d. Ensures: %d. Tasks: %d. Drift: %d. No-ops: %d.", counts["create"], counts["update"], counts["delete"], counts["replace"], counts["ensure"], counts["task"], counts["drift"], counts["noop"]), options.width)
		for _, gap := range gaps {
			writeWrapped(&output, "Uncertainty: "+gap, options.width)
		}
		for _, row := range rows {
			line := paintToken(enabled, palette, "action-"+row.Action, row.Action) + " " + paintToken(enabled, palette, row.Class, "["+row.Class+"]") + " " + row.System + " " + row.Address
			if row.Detail != "" {
				line += " " + row.Detail
			}
			writeWrapped(&output, redactLine(line), options.width)
		}
		if _, err := io.WriteString(stdout, output.String()); err != nil {
			return err
		}
	}
	if status != finding.StatusClean {
		return reportError{status: status}
	}
	return nil
}

func planRows(plan tofuPlan) []prebuildRow {
	rows := []prebuildRow{}
	add := func(system, address, kind string, actions []string) {
		action := planAction(actions)
		rows = append(rows, prebuildRow{Action: action, Class: resourceClass(kind), System: system, Address: address, Detail: "actions " + strings.Join(actions, ",")})
	}
	for _, resource := range plan.ResourceChanges {
		add("tofu", resource.Address, resource.Type, resource.Change.Actions)
	}
	for _, resource := range plan.ResourceDrift {
		rows = append(rows, prebuildRow{Action: "drift", Class: resourceClass(resource.Type), System: "tofu", Address: resource.Address, Detail: "observed drift; values withheld"})
	}
	names := make([]string, 0, len(plan.OutputChanges))
	for name := range plan.OutputChanges {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		change := plan.OutputChanges[name]
		if planAction(change.Actions) == "noop" {
			continue
		}
		rows = append(rows, prebuildRow{Action: planAction(change.Actions), Class: "devops", System: "tofu", Address: "output." + name, Detail: "output values withheld"})
	}
	return rows
}

func planAction(actions []string) string {
	joined := strings.Join(actions, ",")
	switch joined {
	case "create", "update", "delete", "read":
		return joined
	case "no-op", "":
		return "noop"
	case "delete,create", "create,delete":
		return "replace"
	default:
		if len(actions) == 0 {
			return "noop"
		}
		return "unknown"
	}
}

func resourceClass(kind string) string {
	value := strings.ToLower(kind)
	switch {
	case strings.Contains(value, "security_group"), strings.Contains(value, "network_acl"), strings.Contains(value, "firewall"), strings.Contains(value, "network_security"):
		return "filter"
	case strings.Contains(value, "load_balancer"), strings.Contains(value, "_lb"), strings.Contains(value, "application_gateway"):
		return "lb"
	case strings.Contains(value, "route"):
		return "route"
	case strings.Contains(value, "container"), strings.Contains(value, "ecs_"), strings.Contains(value, "pod"):
		return "container"
	case strings.Contains(value, "virtual_machine"), strings.Contains(value, "compute_instance"), strings.HasSuffix(value, "_instance"), strings.Contains(value, "aws_instance"):
		return "vm"
	case strings.Contains(value, "log"):
		return "logs"
	case strings.Contains(value, "directory"):
		return "dir"
	case strings.Contains(value, "bucket"), strings.Contains(value, "s3_"), strings.Contains(value, "storage_object"), strings.Contains(value, "local_file"):
		return "fs"
	default:
		return "devops"
	}
}

func playbookRows(plays []map[string]any) ([]prebuildRow, error) {
	rows := []prebuildRow{}
	for index, play := range plays {
		if _, ok := play["roles"]; ok {
			rows = append(rows, prebuildRow{Action: "unknown", Class: "devops", System: "ansible", Address: fmt.Sprintf("play%d.roles", index+1), Detail: "roles were not expanded"})
		}
		tasks, _ := play["tasks"].([]any)
		for taskIndex, task := range tasks {
			item, ok := task.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("play %d task %d is not a mapping", index+1, taskIndex+1)
			}
			module, args := ansibleModule(item)
			rows = append(rows, ansiblePrebuildRow(fmt.Sprintf("play%d.task%d", index+1, taskIndex+1), module, args))
		}
	}
	return rows, nil
}

func ansiblePrebuildRow(address, module string, args map[string]any) prebuildRow {
	short := module
	if index := strings.LastIndex(module, "."); index >= 0 {
		short = module[index+1:]
	}
	state, _ := args["state"].(string)
	row := prebuildRow{System: "ansible", Address: address + " " + module, Class: "devops", Action: "task"}
	switch short {
	case "file", "copy", "template", "lineinfile", "blockinfile":
		row.Class = "fs"
		if state == "directory" {
			row.Class = "dir"
		}
		row.Action = "ensure"
		if state == "absent" {
			row.Action = "delete"
		}
		row.Detail = ansiblePathDetail(args)
	case "service", "systemd", "sysvinit":
		row.Class = "proc"
		row.Action = "ensure"
		if state == "stopped" || state == "absent" {
			row.Action = "delete"
		}
	case "docker_container", "podman_container":
		row.Class = "container"
		row.Action = "ensure"
		if state == "absent" {
			row.Action = "delete"
		}
	case "wait_for", "uri", "get_url":
		row.Class = "conn"
	case "debug":
		row.Class = "logs"
		row.Detail = "message withheld"
	}
	if row.Detail == "" {
		row.Detail = "module " + short
	}
	return row
}

func ansiblePathDetail(args map[string]any) string {
	for _, key := range []string{"path", "dest", "src"} {
		value, ok := args[key].(string)
		if !ok || value == "" {
			continue
		}
		if strings.Contains(value, "{{") || !operationLabel(value) {
			return "path templated or unsafe"
		}
		return "path " + value
	}
	return "path not literal"
}
