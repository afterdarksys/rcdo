package toolkit

import (
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"git-tools/finding"
)

var gcpProjectPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{4,28}[a-z0-9]$`)
var gcpZonePattern = regexp.MustCompile(`^[a-z]+-[a-z]+[0-9]+-[a-z]$`)
var gcpNamePattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,61}[a-z0-9]$|^[a-z]$`)
var gcpPrincipalPattern = regexp.MustCompile(`^[A-Za-z0-9._%+\-]+@[A-Za-z0-9.-]+$`)
var gcpPathSegmentPattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9-]{0,62}$`)

type gcpSelectors struct{ Project, Principal, Zone, Configuration string }

func validateGCPSelectors(s gcpSelectors, zoneRequired bool) error {
	if !gcpProjectPattern.MatchString(s.Project) || !gcpPrincipalPattern.MatchString(s.Principal) || (zoneRequired && s.Zone == "") || (s.Zone != "" && !gcpZonePattern.MatchString(s.Zone)) || (s.Configuration != "" && !gcpNamePattern.MatchString(s.Configuration)) {
		return fmt.Errorf("GCP requires a project ID, expected account email, and a valid explicit zone for inventory")
	}
	return nil
}
func gcpArgs(s gcpSelectors, pin bool, args ...string) []string {
	out := append([]string{}, args...)
	out = append(out, "--format=json", "--quiet")
	if s.Configuration != "" {
		out = append(out, "--configuration="+s.Configuration)
	}
	if pin {
		out = append(out, "--project="+s.Project, "--account="+s.Principal)
	}
	return out
}

// CLI account metadata and project access are observed independently. Overrides
// which prevent attributing the effective principal are not silently accepted.
func acquireGCP(c *contextAcquirer, s gcpSelectors) (map[string]string, error) {
	if e := validateGCPSelectors(s, false); e != nil {
		return nil, e
	}
	for _, key := range []string{"CLOUDSDK_AUTH_ACCESS_TOKEN", "CLOUDSDK_AUTH_ACCESS_TOKEN_FILE", "CLOUDSDK_AUTH_CREDENTIAL_FILE_OVERRIDE", "CLOUDSDK_AUTH_IMPERSONATE_SERVICE_ACCOUNT"} {
		if os.Getenv(key) != "" {
			return nil, fmt.Errorf("GCP credential overrides require an explicit identity adapter")
		}
	}
	for _, entry := range os.Environ() {
		key, value, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(key, "CLOUDSDK_API_ENDPOINT_OVERRIDES_") && value != "" {
			return nil, fmt.Errorf("GCP endpoint overrides are unsupported")
		}
	}
	raw, e := c.call("gcloud", gcpArgs(s, false, "config", "list", "--all")...)
	if e != nil {
		return nil, e
	}
	var config map[string]map[string]any
	if collectionJSON(raw, &config) != nil || config["core"]["account"] != s.Principal || config["core"]["project"] != s.Project {
		return nil, fmt.Errorf("GCP configured project/account differs from explicit expectations or is missing")
	}
	for _, key := range []string{"impersonate_service_account", "access_token_file", "credential_file_override", "access_token", "disable_credentials"} {
		value := config["auth"][key]
		if value != nil && value != "" && value != false && value != "false" {
			return nil, fmt.Errorf("GCP credential overrides require an explicit identity adapter")
		}
	}
	for _, v := range config["api_endpoint_overrides"] {
		if v != nil && v != "" {
			return nil, fmt.Errorf("GCP endpoint overrides are unsupported")
		}
	}
	raw, e = c.call("gcloud", gcpArgs(s, false, "auth", "list", "--filter=status:ACTIVE")...)
	if e != nil {
		return nil, e
	}
	var accounts []struct {
		Account string `json:"account"`
		Status  string `json:"status"`
	}
	if collectionJSON(raw, &accounts) != nil || len(accounts) != 1 || accounts[0].Account != s.Principal || accounts[0].Status != "ACTIVE" {
		return nil, fmt.Errorf("GCP active credential account does not match expectation")
	}
	raw, e = c.call("gcloud", gcpArgs(s, true, "projects", "describe", s.Project)...)
	if e != nil {
		return nil, e
	}
	var project struct {
		ID     string `json:"projectId"`
		Number string `json:"projectNumber"`
		State  string `json:"lifecycleState"`
	}
	if collectionJSON(raw, &project) != nil || project.ID != s.Project || !decimalID.MatchString(project.Number) || project.State != "ACTIVE" {
		return nil, fmt.Errorf("GCP project identity, number or lifecycle is invalid")
	}
	values := map[string]string{"project": project.ID, "project_number": project.Number, "account": s.Principal, "principal": s.Principal}
	for _, key := range []string{"region", "zone"} {
		if value, ok := config["compute"][key].(string); ok && operationLabel(value) {
			values[key] = value
		}
	}
	if s.Zone != "" {
		values["zone"] = s.Zone
		values["region"] = s.Zone[:strings.LastIndex(s.Zone, "-")]
	}
	if s.Configuration != "" {
		values["configuration"] = s.Configuration
	}
	return values, nil
}

func runGCPContextCheck(o commonOptions, project, principal, region, zone, configuration string, native bool, stdin io.Reader, stdout io.Writer) error {
	s := gcpSelectors{Project: project, Principal: principal, Configuration: configuration}
	if e := validateGCPSelectors(s, false); e != nil {
		return e
	}
	if zone != "" && !gcpZonePattern.MatchString(zone) {
		return fmt.Errorf("invalid expected GCP zone")
	}
	if region != "" && !regionNamePattern.MatchString(region) {
		return fmt.Errorf("invalid expected GCP region")
	}
	if native && o.input != "-" {
		return fmt.Errorf("GCP --collect cannot accompany --input")
	}
	r := finding.Report{CompletedChecks: []string{"GCP configured account, project and optional compute defaults checked; this is not ADC or signed identity attestation"}}
	var values map[string]string
	var raw []byte
	var e error
	if native {
		values, e = acquireGCP(&contextAcquirer{}, s)
		if e != nil {
			r.IncompleteChecks = append(r.IncompleteChecks, e.Error())
			return emitReportOptions(stdout, o, r)
		}
		values["cloud"] = "gcp"
		raw, e = json.Marshal(values)
	} else {
		raw, e = readInput(o.input, stdin)
		if e == nil {
			e = workflowJSON(raw, &values)
		}
	}
	if e != nil {
		return e
	}
	for _, check := range []struct{ key, want string }{{"cloud", "gcp"}, {"project", project}, {"account", principal}, {"region", region}, {"zone", zone}} {
		if check.want == "" {
			continue
		}
		actual := values[check.key]
		if actual == "" {
			r.IncompleteChecks = append(r.IncompleteChecks, "Missing GCP context field: "+check.key)
		} else if actual != check.want {
			addIAC(&r, "GCP-CONTEXT", finding.SeverityCritical, "GCP context mismatch", "gcp-context", "verify", o.environment, "Mismatched field: "+check.key+"; values withheld")
		} else {
			r.CompletedChecks = append(r.CompletedChecks, "Matched GCP field: "+check.key)
		}
	}
	bindReportSource(&r, o, "cloud-context-check", raw)
	return emitReportOptions(stdout, o, r)
}

type gcpInstance struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Zone        string `json:"zone"`
	MachineType string `json:"machineType"`
	Status      string `json:"status"`
	Interfaces  []struct {
		Name    string `json:"name"`
		Network string `json:"network"`
		Subnet  string `json:"subnetwork"`
	} `json:"networkInterfaces"`
}

func gcpResourcePath(raw string) (string, error) {
	if strings.HasPrefix(raw, "https://") {
		u, e := url.Parse(raw)
		if e != nil || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.RawPath != "" || !oneOf(u.Host, "www.googleapis.com", "compute.googleapis.com") {
			return "", fmt.Errorf("invalid Compute resource URL")
		}
		raw = strings.TrimPrefix(u.Path, "/compute/v1/")
	}
	parts := strings.Split(raw, "/")
	if len(parts) < 4 || parts[0] != "projects" || !gcpProjectPattern.MatchString(parts[1]) {
		return "", fmt.Errorf("invalid Compute resource identity")
	}
	for _, p := range parts {
		if !gcpPathSegmentPattern.MatchString(p) {
			return "", fmt.Errorf("invalid Compute resource segment")
		}
	}
	return raw, nil
}
func collectGCPInstances(c *contextAcquirer, s gcpSelectors, limit int) ([]gcpInstance, []string) {
	raw, e := c.call("gcloud", gcpArgs(s, true, "compute", "instances", "list", "--zones="+s.Zone, "--limit="+strconv.Itoa(limit+1), "--page-size=100")...)
	if e != nil {
		return nil, []string{"GCP Compute inventory failed; diagnostics withheld"}
	}
	var items []gcpInstance
	if collectionJSON(raw, &items) != nil || items == nil {
		return nil, []string{"GCP Compute returned invalid or missing inventory"}
	}
	if len(items) > limit {
		return nil, []string{"GCP instance limit reached; inventory coverage is incomplete"}
	}
	seen := map[string]bool{}
	seenIDs := map[string]bool{}
	prefix := "projects/" + s.Project + "/zones/" + s.Zone
	for i := range items {
		v := &items[i]
		zone, e := gcpResourcePath(v.Zone)
		machine, me := gcpResourcePath(v.MachineType)
		if e != nil || me != nil || zone != prefix || !strings.HasPrefix(machine, prefix+"/machineTypes/") || len(strings.Split(machine, "/")) != 6 || !decimalID.MatchString(v.ID) || !gcpNamePattern.MatchString(v.Name) || !oneOf(v.Status, "PROVISIONING", "STAGING", "RUNNING", "STOPPING", "SUSPENDING", "SUSPENDED", "REPAIRING", "TERMINATED") || v.Interfaces == nil || len(v.Interfaces) == 0 || seen[v.Name] || seenIDs[v.ID] {
			return nil, []string{"GCP instance identity, zone, machine type or interface coverage is invalid"}
		}
		seen[v.Name] = true
		seenIDs[v.ID] = true
		v.Zone = zone
		v.MachineType = machine
		for j := range v.Interfaces {
			n := &v.Interfaces[j]
			network, ne := gcpResourcePath(n.Network)
			subnet, se := gcpResourcePath(n.Subnet)
			if ne != nil || se != nil || len(strings.Split(network, "/")) != 5 || len(strings.Split(subnet, "/")) != 6 || !strings.HasPrefix(network, "projects/"+s.Project+"/global/networks/") || !strings.HasPrefix(subnet, "projects/"+s.Project+"/regions/"+s.Zone[:strings.LastIndex(s.Zone, "-")]+"/subnetworks/") {
				return nil, []string{"GCP network/subnet attachment is missing, outside scope, or unsupported (including Shared VPC and legacy networks)"}
			}
			n.Network = network
			n.Subnet = subnet
		}
	}
	return items, nil
}

func runGCPCollect(kind string, s gcpSelectors, limit int, manifest, output string, stdout, stderr io.Writer) error {
	if e := validateGCPSelectors(s, kind != "context"); e != nil {
		return e
	}
	if limit < 1 || limit > 10000 {
		return fmt.Errorf("GCP max-instances must be 1..10000")
	}
	var fleet fleetBundle
	if kind == "fleet" {
		if manifest == "" {
			return fmt.Errorf("GCP fleet requires --manifest")
		}
		raw, e := readConfigSource(manifest)
		if e != nil {
			return e
		}
		if workflowJSON(raw, &fleet) != nil {
			return fmt.Errorf("invalid fleet manifest")
		}
		fleet.Observations = []fleetObservation{}
		if _, e = compareFleet(fleet, time.Hour, time.Hour, time.Now()); e != nil {
			return e
		}
		for _, h := range fleet.Hosts {
			if h.Platform != "gcp-compute" || !strings.HasPrefix(h.ID, "projects/"+s.Project+"/zones/"+s.Zone+"/instances/") {
				return fmt.Errorf("GCP fleet hosts require gcp-compute and exact project/zone instance paths")
			}
		}
	} else if manifest != "" {
		return fmt.Errorf("manifest is only valid for fleet")
	}
	c := &contextAcquirer{}
	values, e := acquireGCP(c, s)
	gaps := []string{}
	items := []gcpInstance{}
	if e != nil {
		gaps = append(gaps, e.Error())
	} else if kind != "context" {
		items, gaps = collectGCPInstances(c, s, limit)
	}
	if e == nil {
		after, err := acquireGCP(c, s)
		if err != nil || after["project_number"] != values["project_number"] {
			items = nil
			gaps = append(gaps, "GCP identity changed or could not be rechecked; inventory discarded")
		}
	}
	complete := len(gaps) == 0
	stamp := time.Now().UTC().Format(time.RFC3339Nano)
	source := "gcloud read-only observation; project " + s.Project + "; zone " + s.Zone + "; instance control-plane metadata only, not guest health"
	var artifact any
	switch kind {
	case "context":
		b := contextBundle{SchemaVersion: "1", Complete: &complete, Contexts: map[string]contextObservation{}}
		if values != nil {
			outcome := "pass"
			if !complete {
				outcome = "error"
			}
			b.Contexts["gcp"] = contextObservation{Kind: "gcp", Values: values, CollectedAt: stamp, Source: source, Outcome: outcome, Provenance: c.proof}
		}
		artifact = b
	case "fleet":
		wanted := map[string]bool{}
		for _, h := range fleet.Hosts {
			wanted[h.ID] = true
		}
		for _, v := range items {
			id := v.Zone + "/instances/" + v.Name
			if !wanted[id] {
				continue
			}
			delete(wanted, id)
			fleet.Observations = append(fleet.Observations, fleetObservation{Host: id, Platform: "gcp-compute", Outcome: "pass", Source: source, CollectedAt: stamp, Values: map[string]any{"instance_id": v.ID, "machine_type": v.MachineType, "state": v.Status, "zone": s.Zone, "project": s.Project}})
		}
		for id := range wanted {
			gaps = append(gaps, "Required GCP instance not observed: "+id)
		}
		complete = len(gaps) == 0
		fleet.Complete = &complete
		artifact = fleet
	case "relations":
		g := relationGraph{SchemaVersion: "1", Complete: &complete, CollectedAt: stamp, Source: source + "; network/subnet references are not independently described", Nodes: []relationNode{}, Edges: []relationEdge{}, Scopes: []relationScope{{Account: s.Project, Region: s.Zone, Complete: &complete, Outcome: "pass"}}}
		nodes := map[string]relationNode{}
		edges := map[string]bool{}
		add := func(id, kind string) {
			nodes[id] = relationNode{ID: id, Type: kind, Account: s.Project, Region: s.Zone}
		}
		for _, v := range items {
			id := v.Zone + "/instances/" + v.Name
			add(id, "gcp-compute-instance")
			for _, n := range v.Interfaces {
				for ref, kind := range map[string]string{n.Network: "gcp-network-reference", n.Subnet: "gcp-subnet-reference"} {
					add(ref, kind)
					key := id + "/" + ref
					if !edges[key] {
						g.Edges = append(g.Edges, relationEdge{From: id, To: ref, Kind: "observed", Source: "Compute instance networkInterfaces"})
						edges[key] = true
					}
				}
			}
		}
		if len(nodes) > 10000 || len(g.Edges) > 50000 {
			complete = false
			gaps = append(gaps, "GCP relationship graph limit exceeded")
			g.Edges = []relationEdge{}
			nodes = map[string]relationNode{}
		}
		for _, id := range sortedRelationNodes(nodes) {
			g.Nodes = append(g.Nodes, nodes[id])
		}
		sort.Slice(g.Edges, func(i, j int) bool { return g.Edges[i].From+"/"+g.Edges[i].To < g.Edges[j].From+"/"+g.Edges[j].To })
		if !complete {
			g.Scopes[0].Outcome = "error"
		}
		artifact = g
	default:
		return fmt.Errorf("unknown GCP collection kind")
	}
	raw, e := json.MarshalIndent(artifact, "", "  ")
	if e != nil {
		return e
	}
	raw = append(raw, '\n')
	if len(raw) > 16<<20 {
		return fmt.Errorf("GCP artifact exceeds 16 MiB")
	}
	if output == "-" {
		_, e = stdout.Write(raw)
	} else {
		e = publishMarkdown(output, raw)
	}
	if e != nil {
		return e
	}
	for _, g := range gaps {
		fmt.Fprintln(stderr, safeReportText("Incomplete: "+g))
	}
	if !complete {
		return reportError{status: finding.StatusIncomplete}
	}
	return nil
}
