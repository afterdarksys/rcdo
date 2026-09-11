package toolkit

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"regexp"
)

var gcpRegionPattern = regexp.MustCompile(`^[a-z]+-[a-z]+[0-9]+$`)
var gcpBucketPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{1,61}[a-z0-9]$`)

func runGCPCommandGen(input, format, shell string, width int, stdin io.Reader, stdout io.Writer) error {
	raw, e := readInput(input, stdin)
	if e != nil {
		return e
	}
	if len(raw) > 1<<20 {
		return fmt.Errorf("GCP command request exceeds 1 MiB")
	}
	var request map[string]string
	if workflowDocument(raw, &request) != nil {
		return fmt.Errorf("GCP preview requires a JSON/YAML object of literal string fields")
	}
	if e = validateGCPSelectors(gcpSelectors{Project: request["project"], Principal: request["account"], Configuration: request["configuration"]}, false); e != nil {
		return e
	}
	resource, name := request["resource"], request["name"]
	allowed := flagSet("project", "account", "configuration", "resource", "name")
	argv := []string{"gcloud"}
	notes := []string{"Preview only; no Google Cloud command was executed. Verify project and principal before running."}
	switch resource {
	case "network":
		if !gcpNamePattern.MatchString(name) {
			return fmt.Errorf("invalid network name")
		}
		allowed["subnet_mode"] = true
		mode := request["subnet_mode"]
		if mode == "" {
			mode = "custom"
		}
		if !oneOf(mode, "custom", "auto") {
			return fmt.Errorf("subnet_mode must be custom or auto")
		}
		argv = append(argv, "compute", "networks", "create", name, "--subnet-mode="+mode)
	case "subnet":
		for _, k := range []string{"region", "network", "range"} {
			allowed[k] = true
		}
		ip, _, err := net.ParseCIDR(request["range"])
		if !gcpNamePattern.MatchString(name) || !gcpNamePattern.MatchString(request["network"]) || !gcpRegionPattern.MatchString(request["region"]) || err != nil || ip.To4() == nil {
			return fmt.Errorf("subnet requires literal name, same-project network, region and IPv4 CIDR range")
		}
		argv = append(argv, "compute", "networks", "subnets", "create", name, "--network="+request["network"], "--region="+request["region"], "--range="+request["range"])
	case "bucket":
		allowed["location"] = true
		if !gcpBucketPattern.MatchString(name) || (!gcpRegionPattern.MatchString(request["location"]) && !oneOf(request["location"], "US", "EU", "ASIA")) {
			return fmt.Errorf("bucket requires a valid name and explicit regional or multi-region location")
		}
		argv = append(argv, "storage", "buckets", "create", "gs://"+name, "--location="+request["location"], "--uniform-bucket-level-access", "--public-access-prevention")
		notes = append(notes, "This recipe enforces uniform bucket-level access and public access prevention. Provider naming/availability and organization policies still apply.")
	case "service-account":
		allowed["display_name"] = true
		if !gcpNamePattern.MatchString(name) || len(name) < 6 || len(name) > 30 {
			return fmt.Errorf("service-account name must be 6..30 lowercase letters/digits/hyphens")
		}
		argv = append(argv, "iam", "service-accounts", "create", name)
		if request["display_name"] != "" {
			argv = append(argv, "--display-name="+request["display_name"])
		}
		notes = append(notes, "Creates a service account only; grants no IAM roles and creates no keys.")
	default:
		return fmt.Errorf("supported GCP create resources: network, subnet, bucket, service-account")
	}
	for key := range request {
		if !allowed[key] {
			return fmt.Errorf("unsupported field %q for GCP %s", key, resource)
		}
	}
	argv = append(argv, "--project="+request["project"], "--account="+request["account"], "--format=json")
	if request["configuration"] != "" {
		argv = append(argv, "--configuration="+request["configuration"])
	}
	command, e := quoteCommand(argv, shell)
	if e != nil {
		return e
	}
	recipe := commandRecipe{SchemaVersion: "1", Target: "gcp", Action: "create", SourceSHA256: digestBytes(raw), Executed: false, Mutating: true, Argv: argv, Command: command, Notes: notes}
	explainRecipe(&recipe, shell)
	if format == "json" {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(recipe)
	}
	renderExplainedRecipe(stdout, recipe, width)
	return nil
}
