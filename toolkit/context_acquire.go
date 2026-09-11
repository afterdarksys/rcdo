package toolkit

import (
	"encoding/json"
	"flag"
	"fmt"
	"git-tools/finding"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

type contextAcquirer struct{ proof []string }

func (c *contextAcquirer) call(name string, args ...string) ([]byte, error) {
	r := executeReadOnly(name, args...)
	if r.err != nil {
		return nil, fmt.Errorf("%s acquisition failed; native diagnostics withheld", name)
	}
	c.proof = append(c.proof, name+" "+strings.Join(auditArguments(args), " ")+"; output SHA-256 "+digestBytes(r.stdout))
	return r.stdout, nil
}
func (c *contextAcquirer) textJSON(name string, args ...string) (string, error) {
	raw, err := c.call(name, args...)
	if err != nil {
		return "", err
	}
	var v string
	if json.Unmarshal(raw, &v) != nil || !operationLabel(v) {
		return "", fmt.Errorf("%s returned missing identity", name)
	}
	return v, nil
}

func acquireDocker(c *contextAcquirer, name string) (map[string]string, error) {
	if !operationLabel(name) || strings.HasPrefix(name, "-") {
		return nil, fmt.Errorf("explicit --docker-context is required")
	}
	endpoint, err := c.textJSON("docker", "--context", name, "context", "inspect", name, "--format", "{{json .Endpoints.docker.Host}}")
	if err != nil {
		return nil, err
	}
	id, err := c.textJSON("docker", "--context", name, "info", "--format", "{{json .ID}}")
	if err != nil {
		return nil, err
	}
	again, err := c.textJSON("docker", "--context", name, "context", "inspect", name, "--format", "{{json .Endpoints.docker.Host}}")
	if err != nil || again != endpoint {
		return nil, fmt.Errorf("Docker endpoint changed during acquisition")
	}
	if auditText(endpoint) != endpoint {
		return nil, fmt.Errorf("Docker endpoint contains credential material or controls")
	}
	return map[string]string{"endpoint": endpoint, "daemon_id": id}, nil
}
func acquireIAC(c *contextAcquirer, engine, directory string) (map[string]string, error) {
	dir, err := filepath.Abs(directory)
	if err != nil {
		return nil, err
	}
	workspace, err := c.call(engine, "-chdir="+dir, "workspace", "show")
	if err != nil {
		return nil, err
	}
	w := strings.TrimSpace(string(workspace))
	if !operationLabel(w) {
		return nil, fmt.Errorf("workspace unavailable")
	}
	version, err := c.call(engine, "version", "-json")
	if err != nil {
		return nil, err
	}
	var v struct {
		Version string `json:"terraform_version"`
	}
	if collectionJSON(version, &v) != nil || !operationLabel(v.Version) {
		return nil, fmt.Errorf("engine version unavailable")
	}
	dataDir := os.Getenv("TF_DATA_DIR")
	if dataDir == "" {
		dataDir = filepath.Join(dir, ".terraform")
	} else if !filepath.IsAbs(dataDir) {
		dataDir = filepath.Join(dir, dataDir)
	}
	path := filepath.Join(dataDir, "terraform.tfstate")
	binding, raw, err := captureArtifact(path)
	if err != nil {
		return nil, fmt.Errorf("initialized backend metadata unavailable")
	}
	var metadata struct {
		Backend *struct {
			Type   string         `json:"type"`
			Config map[string]any `json:"config"`
		} `json:"backend"`
	}
	if collectionJSON(raw, &metadata) != nil || metadata.Backend == nil || !operationLabel(metadata.Backend.Type) {
		return nil, fmt.Errorf("backend metadata lacks a known type")
	}
	values := map[string]string{"workspace": w, "engine_version": v.Version, "backend": metadata.Backend.Type}
	for key, target := range map[string]string{"key": "backend_key", "bucket": "backend_bucket", "region": "backend_region", "container_name": "backend_container", "storage_account_name": "backend_account", "prefix": "backend_prefix"} {
		if value, ok := metadata.Backend.Config[key].(string); ok && value != "" {
			if auditText(value) != value || !operationLabel(value) {
				return nil, fmt.Errorf("unsafe backend identity field")
			}
			values[target] = value
		}
	}
	if metadata.Backend.Type == "s3" && w != "default" {
		key, ok := values["backend_key"]
		prefix := "env:"
		if p, ok := metadata.Backend.Config["workspace_key_prefix"].(string); ok {
			prefix = p
		}
		if ok {
			values["backend_key"] = strings.TrimSuffix(prefix, "/") + "/" + w + "/" + key
		}
	}
	if _, err := readBoundArtifact(binding); err != nil {
		return nil, err
	}
	again, err := c.call(engine, "-chdir="+dir, "workspace", "show")
	if err != nil || strings.TrimSpace(string(again)) != w {
		return nil, fmt.Errorf("workspace changed during acquisition")
	}
	c.proof = append(c.proof, "Initialized local backend metadata SHA-256 "+binding.SHA256+"; backend credentials and state values withheld; remote backend reachability not established")
	return values, nil
}
func acquireInventory(c *contextAcquirer, input string) (map[string]string, error) {
	if input == "" {
		return nil, fmt.Errorf("--inventory file is required")
	}
	source, _, err := captureArtifact(input)
	if err != nil {
		return nil, err
	}
	raw, err := c.call("ansible-inventory", "-i", source.Path, "--list")
	if err != nil {
		return nil, err
	}
	var inventory map[string]json.RawMessage
	if collectionJSON(raw, &inventory) != nil {
		return nil, fmt.Errorf("invalid inventory output")
	}
	hosts := map[string]bool{}
	for group, data := range inventory {
		if group == "_meta" {
			var m struct {
				Hostvars map[string]json.RawMessage `json:"hostvars"`
			}
			if json.Unmarshal(data, &m) != nil {
				return nil, fmt.Errorf("invalid host metadata")
			}
			for h := range m.Hostvars {
				hosts[h] = true
			}
		} else {
			var g struct {
				Hosts []string `json:"hosts"`
			}
			if json.Unmarshal(data, &g) != nil {
				return nil, fmt.Errorf("invalid inventory group")
			}
			for _, h := range g.Hosts {
				hosts[h] = true
			}
		}
	}
	if len(hosts) == 0 {
		return nil, fmt.Errorf("inventory resolved no hosts; inspect parser/plugin coverage")
	}
	names := []string{}
	for h := range hosts {
		if !operationLabel(h) {
			return nil, fmt.Errorf("invalid host identity")
		}
		names = append(names, h)
	}
	sort.Strings(names)
	normalized, _ := json.Marshal(names)
	if _, err := readBoundArtifact(source); err != nil {
		return nil, err
	}
	c.proof = append(c.proof, "Inventory source SHA-256 "+source.SHA256+"; host variables withheld; playbook limits, remote user and reachability not established")
	return map[string]string{"inventory_sha256": source.SHA256, "inventory_hosts_sha256": digestBytes(normalized), "host_count": strconv.Itoa(len(names))}, nil
}

const spaceContextQuery = `query RCDOContext($stack: ID!, $run: ID!) { stack(id: $stack) { id run(id: $run) { id state type needsApproval isMostRecent commit { hash } } } }`

func acquireSpaceContext(c *contextAcquirer, stack, run, expectedEndpoint string) (map[string]string, error) {
	if !operationLabel(stack) || !operationLabel(run) || expectedEndpoint == "" {
		return nil, fmt.Errorf("Spacelift requires --stack, --run and --expect-endpoint")
	}
	type identity struct {
		ID       string `json:"id"`
		Endpoint string `json:"endpoint"`
	}
	who, err := c.call("spacectl", "whoami")
	if err != nil {
		return nil, err
	}
	var user identity
	if collectionJSON(who, &user) != nil || user.Endpoint != expectedEndpoint || !operationLabel(user.ID) {
		return nil, fmt.Errorf("Spacelift identity or endpoint mismatch; run not queried")
	}
	endpoint, err := url.Parse(user.Endpoint)
	if err != nil || endpoint.Scheme != "https" || endpoint.Host == "" || endpoint.User != nil || endpoint.RawQuery != "" {
		return nil, fmt.Errorf("invalid Spacelift endpoint")
	}
	vars, _ := json.Marshal(map[string]string{"stack": stack, "run": run})
	raw, err := c.call("spacectl", "api", "--raw", "--variables", string(vars), spaceContextQuery)
	if err != nil {
		return nil, err
	}
	var response struct {
		Errors []json.RawMessage `json:"errors"`
		Data   struct {
			Stack *struct {
				ID  string `json:"id"`
				Run *struct {
					ID            string `json:"id"`
					State         string `json:"state"`
					NeedsApproval *bool  `json:"needsApproval"`
					IsMostRecent  *bool  `json:"isMostRecent"`
					Commit        struct {
						Hash string `json:"hash"`
					} `json:"commit"`
				} `json:"run"`
			} `json:"stack"`
		} `json:"data"`
	}
	if collectionJSON(raw, &response) != nil || len(response.Errors) > 0 || response.Data.Stack == nil || response.Data.Stack.Run == nil {
		return nil, fmt.Errorf("Spacelift returned partial run context")
	}
	s := response.Data.Stack
	r := s.Run
	if s.ID != stack || r.ID != run || !fullCommitPattern.MatchString(r.Commit.Hash) || r.NeedsApproval == nil || r.IsMostRecent == nil || !operationLabel(r.State) {
		return nil, fmt.Errorf("Spacelift run binding or approval fields unavailable")
	}
	final, err := c.call("spacectl", "whoami")
	var second identity
	if err != nil || collectionJSON(final, &second) != nil || second != user {
		return nil, fmt.Errorf("Spacelift authentication changed during acquisition")
	}
	return map[string]string{"account": endpoint.Hostname(), "endpoint": user.Endpoint, "principal": user.ID, "stack": s.ID, "run": r.ID, "commit": r.Commit.Hash, "state": r.State, "needs_approval": strconv.FormatBool(*r.NeedsApproval), "is_most_recent": strconv.FormatBool(*r.IsMostRecent)}, nil
}
func runContextAcquire(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("context-acquire", flag.ContinueOnError)
	fs.SetOutput(stderr)
	kind := fs.String("kind", "", "docker, tofu, terraform, ansible, spacelift or gcp")
	project := fs.String("project", "", "expected GCP project ID")
	principal := fs.String("expect-account", "", "expected GCP account email")
	zone := fs.String("zone", "", "optional explicit GCP zone")
	configuration := fs.String("configuration", "", "gcloud named configuration")
	native := fs.Bool("native", false, "execute read-only native CLI acquisition; inventory plugins may execute locally")
	name := fs.String("name", "", "context name in bundle")
	output := fs.String("output", "-", "new bundle file or stdout")
	merge := fs.String("merge", "", "existing bundle to copy other named contexts from")
	docker := fs.String("docker-context", "", "explicit Docker context")
	dir := fs.String("directory", ".", "initialized IaC module directory")
	inventory := fs.String("inventory", "", "Ansible inventory file")
	stack := fs.String("stack", "", "Spacelift stack ID")
	run := fs.String("run", "", "Spacelift run ID")
	endpoint := fs.String("expect-endpoint", "", "exact expected spacectl endpoint")
	setAccessibleUsage(fs, "context-acquire", stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 || !*native || !oneOf(*kind, "docker", "tofu", "terraform", "ansible", "spacelift", "gcp") || !operationLabel(*name) {
		return fmt.Errorf("requires --native, --name and supported --kind")
	}
	complete := true
	if *kind != "gcp" && (*project != "" || *principal != "" || *zone != "" || *configuration != "") {
		return fmt.Errorf("GCP selectors require --kind gcp")
	}
	bundle := contextBundle{SchemaVersion: "1", Complete: &complete, Contexts: map[string]contextObservation{}}
	if *merge != "" {
		raw, err := readConfigSource(*merge)
		if err != nil {
			return err
		}
		bundle, err = readContextBundle(raw)
		if err != nil {
			return err
		}
		if _, ok := bundle.Contexts[*name]; ok {
			return fmt.Errorf("context name already exists; use a new evidence bundle")
		}
	}
	c := &contextAcquirer{}
	var values map[string]string
	var err error
	switch *kind {
	case "gcp":
		values, err = acquireGCP(c, gcpSelectors{Project: *project, Principal: *principal, Zone: *zone, Configuration: *configuration})
	case "docker":
		values, err = acquireDocker(c, *docker)
	case "tofu", "terraform":
		values, err = acquireIAC(c, *kind, *dir)
	case "ansible":
		values, err = acquireInventory(c, *inventory)
	case "spacelift":
		values, err = acquireSpaceContext(c, *stack, *run, *endpoint)
	}
	if err != nil {
		complete = false
		bundle.Complete = &complete
		fmt.Fprintln(stderr, auditText(err.Error()))
	} else if !contextValuesValid(*kind, values) {
		return fmt.Errorf("collector returned invalid context fields")
	} else {
		bundle.Contexts[*name] = contextObservation{Kind: *kind, Values: values, CollectedAt: time.Now().UTC().Format(time.RFC3339Nano), Source: "rcdo context-acquire: native CLI and bounded local metadata", Outcome: "pass", Provenance: c.proof}
	}
	raw, e := json.MarshalIndent(bundle, "", "  ")
	if e != nil {
		return e
	}
	raw = append(raw, '\n')
	if *output == "-" {
		_, e = stdout.Write(raw)
	} else {
		e = publishMarkdown(*output, raw)
	}
	if e != nil {
		return e
	}
	if err != nil || bundle.Complete == nil || !*bundle.Complete {
		return reportError{status: finding.StatusIncomplete}
	}
	return nil
}
