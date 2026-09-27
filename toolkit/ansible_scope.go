package toolkit

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"sort"
	"strings"

	"git-tools/finding"
	"gopkg.in/yaml.v3"
)

// ansible-scope reads play text and an optional ansible-inventory --list JSON
// file. It expands a literal host or group name. It does not apply group vars,
// host vars, extra vars, ansible.cfg, or become plugins, and it does not walk
// role files. Templated limits and users stay incomplete.

func runAnsibleScope(args []string, stdout, stderr io.Writer) error {
	var playbook, inventory string
	_, options, err := parseFlags("ansible-scope", args, stderr, func(fs *flag.FlagSet) *commonOptions {
		var options commonOptions
		addCommonFlags(fs, &options)
		fs.StringVar(&playbook, "playbook", "", "Ansible playbook YAML")
		fs.StringVar(&inventory, "inventory", "", "ansible-inventory --list JSON; host vars are ignored")
		return &options
	})
	if err != nil {
		return err
	}
	if playbook == "" || options.input != "-" {
		return fmt.Errorf("ansible-scope requires --playbook and does not read --input")
	}
	data, err := readConfigSource(playbook)
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
	var groups map[string][]string
	var childGroups map[string]bool
	if inventory != "" {
		raw, err := readConfigSource(inventory)
		if err != nil {
			return err
		}
		groups, childGroups, err = readAnsibleGroupHosts(raw)
		if err != nil {
			return err
		}
	}
	report := finding.Report{CompletedChecks: []string{"Play-level scope only. Group vars, host vars, extra vars, ansible.cfg, and become plugins were not applied."}, IncompleteChecks: []string{"Effective remote user was not resolved beyond the play text."}}
	for index, play := range plays {
		label := fmt.Sprintf("Play %d", index+1)
		hosts, ok := play["hosts"].(string)
		if !ok || !operationLabel(hosts) {
			report.IncompleteChecks = append(report.IncompleteChecks, label+": hosts pattern is missing or not a single line")
			continue
		}
		if strings.Contains(hosts, "{{") || inventory == "" {
			reason := "inventory was not supplied"
			if strings.Contains(hosts, "{{") {
				reason = "hosts pattern is templated"
			}
			report.IncompleteChecks = append(report.IncompleteChecks, label+": play limit was not expanded; "+reason)
		} else if expanded, note, ok := expandAnsibleLimit(hosts, groups, childGroups); !ok {
			report.IncompleteChecks = append(report.IncompleteChecks, label+": play limit was not expanded; "+note)
		} else {
			report.CompletedChecks = append(report.CompletedChecks, label+" limit: "+strings.Join(expanded, ", ")+". Pattern: "+hosts+".")
		}
		reportPlayUser(&report, label, "remote_user", play["remote_user"])
		reportPlayUser(&report, label, "become_user", play["become_user"])
		if _, ok := play["roles"]; ok {
			report.IncompleteChecks = append(report.IncompleteChecks, label+": roles were not expanded")
		}
		if tasks, ok := play["tasks"].([]any); ok {
			for _, task := range tasks {
				item, ok := task.(map[string]any)
				if !ok {
					report.IncompleteChecks = append(report.IncompleteChecks, label+": a task was not a mapping")
					continue
				}
				if _, ok := item["remote_user"]; ok {
					report.IncompleteChecks = append(report.IncompleteChecks, label+": a task sets remote_user; the play value is not the only user")
				}
				if _, ok := item["become_user"]; ok {
					report.IncompleteChecks = append(report.IncompleteChecks, label+": a task sets become_user; the play value is not the only user")
				}
			}
		}
	}
	return emitReportOptions(stdout, options, report)
}

func readAnsiblePlays(data []byte) ([]map[string]any, error) {
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	var plays []map[string]any
	if err := decoder.Decode(&plays); err != nil || len(plays) == 0 || len(plays) > 64 {
		return nil, fmt.Errorf("playbook must be one YAML list of 1..64 plays")
	}
	var extra yaml.Node
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, fmt.Errorf("playbook must contain one YAML document")
	}
	return plays, nil
}

func readAnsibleGroupHosts(data []byte) (map[string][]string, map[string]bool, error) {
	if len(data) > 2<<20 || validateConfigDocument("json", data) != nil {
		return nil, nil, fmt.Errorf("invalid inventory JSON")
	}
	var document map[string]json.RawMessage
	if json.Unmarshal(data, &document) != nil {
		return nil, nil, fmt.Errorf("inventory must be an object")
	}
	groups := map[string][]string{}
	children := map[string]bool{}
	for name, raw := range document {
		if name == "_meta" {
			continue
		}
		if !operationLabel(name) {
			return nil, nil, fmt.Errorf("invalid inventory group name")
		}
		var group struct {
			Hosts    []string `json:"hosts"`
			Children []string `json:"children"`
		}
		if json.Unmarshal(raw, &group) != nil {
			return nil, nil, fmt.Errorf("inventory group %s is not a host list", name)
		}
		if len(group.Children) > 0 {
			children[name] = true
		}
		for _, host := range group.Hosts {
			if !operationLabel(host) || strings.Contains(host, "{{") {
				return nil, nil, fmt.Errorf("invalid inventory host name")
			}
		}
		groups[name] = append([]string{}, group.Hosts...)
	}
	return groups, children, nil
}

func expandAnsibleLimit(pattern string, groups map[string][]string, children map[string]bool) ([]string, string, bool) {
	if strings.ContainsAny(pattern, ":*!&") {
		return nil, "pattern uses a wildcard, intersection, or exclusion", false
	}
	seen := map[string]bool{}
	var hosts []string
	add := func(host string) {
		if !seen[host] {
			seen[host] = true
			hosts = append(hosts, host)
		}
	}
	for _, part := range strings.Split(pattern, ",") {
		part = strings.TrimSpace(part)
		if part == "" || !operationLabel(part) {
			return nil, "empty host pattern", false
		}
		if part == "all" {
			for _, list := range groups {
				for _, host := range list {
					add(host)
				}
			}
			continue
		}
		if list, ok := groups[part]; ok {
			if children[part] {
				return nil, "group " + part + " has children; child groups were not expanded", false
			}
			for _, host := range list {
				add(host)
			}
			continue
		}
		found := false
		for _, list := range groups {
			for _, host := range list {
				if host == part {
					found = true
				}
			}
		}
		if !found {
			return nil, "pattern " + part + " is not a host or group in the inventory", false
		}
		add(part)
	}
	sort.Strings(hosts)
	if len(hosts) == 0 || len(hosts) > 256 {
		return nil, "expanded limit is empty or exceeds 256 hosts", false
	}
	return hosts, "", true
}

func reportPlayUser(report *finding.Report, play, field string, value any) {
	if value == nil {
		report.CompletedChecks = append(report.CompletedChecks, play+" "+field+": not set in the play.")
		return
	}
	text, ok := value.(string)
	if !ok || !operationLabel(text) || strings.Contains(text, "{{") {
		report.IncompleteChecks = append(report.IncompleteChecks, play+": "+field+" is templated or not a single line")
		return
	}
	report.CompletedChecks = append(report.CompletedChecks, play+" "+field+": "+text+". This is the play text, not the effective user.")
}
