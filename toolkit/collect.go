package toolkit

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
	"time"

	"git-tools/finding"
)

var awsAccountID = regexp.MustCompile(`^[0-9]{12}$`)

type collectedInstance struct {
	InstanceID   string `json:"InstanceId"`
	InstanceType string `json:"InstanceType"`
	ImageID      string `json:"ImageId"`
	VpcID        string `json:"VpcId"`
	SubnetID     string `json:"SubnetId"`
	State        struct {
		Name string `json:"Name"`
	} `json:"State"`
	Groups []struct {
		ID string `json:"GroupId"`
	} `json:"SecurityGroups"`
}
type instancePage struct {
	Reservations []struct {
		Instances []collectedInstance `json:"Instances"`
	} `json:"Reservations"`
	NextToken string `json:"NextToken"`
}

func collectionJSON(data []byte, v any) error {
	if len(data) > 16<<20 || validateConfigDocument("json", data) != nil {
		return fmt.Errorf("invalid or oversized collector JSON")
	}
	return json.Unmarshal(data, v)
}
func awsCollectionArgs(region, profile string, args ...string) []string {
	out := append([]string{}, args...)
	out = append(out, "--region", region, "--output", "json", "--no-cli-pager", "--no-cli-auto-prompt", "--cli-connect-timeout", "10", "--cli-read-timeout", "30")
	if profile != "" {
		out = append(out, "--profile", profile)
	}
	return out
}
func collectAWSInstances(region, profile string, maxPages int) ([]collectedInstance, []string) {
	instances := []collectedInstance{}
	seenIDs := map[string]bool{}
	seenTokens := map[string]bool{}
	token := ""
	for page := 0; page < maxPages; page++ {
		args := []string{"ec2", "describe-instances", "--max-items", "100", "--page-size", "100"}
		if token != "" {
			args = append(args, "--starting-token", token)
		}
		result := executeReadOnly("aws", awsCollectionArgs(region, profile, args...)...)
		if result.err != nil {
			return instances, []string{fmt.Sprintf("EC2 collection failed at page %d; raw diagnostics withheld", page+1)}
		}
		var p instancePage
		if collectionJSON(result.stdout, &p) != nil || p.Reservations == nil {
			return instances, []string{"EC2 returned invalid page coverage"}
		}
		for _, reservation := range p.Reservations {
			if reservation.Instances == nil {
				return instances, []string{"EC2 reservation omitted instances"}
			}
			for _, item := range reservation.Instances {
				if !operationLabel(item.InstanceID) || seenIDs[item.InstanceID] {
					return instances, []string{"EC2 returned missing or duplicate instance identity"}
				}
				if len(instances) >= 10000 {
					return instances, []string{"EC2 instance limit reached"}
				}
				seenIDs[item.InstanceID] = true
				instances = append(instances, item)
			}
		}
		if p.NextToken == "" {
			return instances, nil
		}
		if len(p.NextToken) > 16384 || seenTokens[p.NextToken] {
			return instances, []string{"EC2 continuation token repeated or oversized"}
		}
		seenTokens[p.NextToken] = true
		token = p.NextToken
	}
	return instances, []string{"EC2 page limit reached; coverage is incomplete"}
}
func runCollect(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("collect", flag.ContinueOnError)
	fs.SetOutput(stderr)
	kind := fs.String("kind", "context", "context, fleet or relations")
	cloud := fs.String("cloud", "aws", "aws or gcp")
	project := fs.String("project", "", "explicit Google Cloud project ID")
	zone := fs.String("zone", "", "explicit Google Cloud inventory zone")
	configuration := fs.String("configuration", "", "gcloud named configuration")
	maxInstances := fs.Int("max-instances", 1000, "GCP instance limit, 1..10000; limit overflow is incomplete")
	region := fs.String("region", "", "explicit AWS region")
	profile := fs.String("profile", "", "AWS profile; omitted uses CLI credential chain")
	account := fs.String("expect-account", "", "expected AWS account ID or GCP credential account email")
	output := fs.String("output", "-", "new JSON artifact path or - for stdout")
	manifest := fs.String("manifest", "", "fleet bundle supplying required hosts and baselines; observations replaced")
	pages := fs.Int("max-pages", 10, "maximum EC2 CLI pages, 1..100")
	setAccessibleUsage(fs, "collect", stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 || !oneOf(*cloud, "aws", "gcp") || !oneOf(*kind, "context", "fleet", "relations") {
		return fmt.Errorf("invalid cloud or collection kind")
	}
	if *cloud == "gcp" {
		if *region != "" || *profile != "" || hasCLIFlag(args, "max-pages") {
			return fmt.Errorf("GCP uses --zone, --configuration and --max-instances")
		}
		return runGCPCollect(*kind, gcpSelectors{Project: *project, Principal: *account, Zone: *zone, Configuration: *configuration}, *maxInstances, *manifest, *output, stdout, stderr)
	}
	if *project != "" || *zone != "" || *configuration != "" || hasCLIFlag(args, "max-instances") {
		return fmt.Errorf("GCP selectors require --cloud gcp")
	}
	if fs.NArg() != 0 || !oneOf(*kind, "context", "fleet", "relations") || !regionNamePattern.MatchString(*region) || !awsAccountID.MatchString(*account) || (*profile != "" && !profileNamePattern.MatchString(*profile)) || *pages < 1 || *pages > 100 {
		return fmt.Errorf("collect requires --region, --expect-account and valid kind/page limit")
	}
	var fleet fleetBundle
	if *kind == "fleet" {
		if *manifest == "" {
			return fmt.Errorf("fleet requires --manifest")
		}
		if err := readStrictJSONFile(*manifest, &fleet); err != nil {
			return err
		}
		raw, err := readConfigSource(*manifest)
		if err != nil {
			return err
		}
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		if err := decoder.Decode(&fleet); err != nil {
			return err
		}
		fleet.Observations = []fleetObservation{}
		if _, err := compareFleet(fleet, time.Hour, time.Hour, time.Now()); err != nil {
			return err
		}
		for _, h := range fleet.Hosts {
			if h.Platform != "aws-ec2" {
				return fmt.Errorf("EC2 inventory hosts require platform aws-ec2; guest OS health is not collected")
			}
		}
	} else if *manifest != "" {
		return fmt.Errorf("manifest is only valid for fleet collection")
	}
	stamp := time.Now().UTC().Format(time.RFC3339Nano)
	gaps := []string{}
	var identity struct {
		Account string
		Arn     string
	}
	result := executeReadOnly("aws", awsCollectionArgs(*region, *profile, "sts", "get-caller-identity")...)
	if result.err != nil || collectionJSON(result.stdout, &identity) != nil || !awsAccountID.MatchString(identity.Account) || !operationLabel(identity.Arn) {
		gaps = append(gaps, "AWS identity collection failed; no inventory requests were made")
	} else if identity.Account != *account {
		gaps = append(gaps, "AWS account differs from --expect-account; no inventory requests were made")
	}
	instances := []collectedInstance{}
	if len(gaps) == 0 && *kind != "context" {
		var more []string
		instances, more = collectAWSInstances(*region, *profile, *pages)
		gaps = append(gaps, more...)
		// Credentials can change between invocations; check the account/principal again.
		final := executeReadOnly("aws", awsCollectionArgs(*region, *profile, "sts", "get-caller-identity")...)
		var after struct {
			Account string
			Arn     string
		}
		if final.err != nil || collectionJSON(final.stdout, &after) != nil || after.Account != identity.Account || after.Arn != identity.Arn {
			instances = nil
			gaps = append(gaps, "AWS identity changed or could not be rechecked; inventory discarded")
		}
	}
	complete := len(gaps) == 0
	source := "AWS CLI read-only collection at " + stamp + "; region " + *region + "; expected account " + *account
	if len(gaps) > 0 {
		source += "; " + strings.Join(gaps, "; ")
	}
	var artifact any
	switch *kind {
	case "context":
		b := contextBundle{SchemaVersion: "1", Complete: &complete, Contexts: map[string]contextObservation{}}
		if awsAccountID.MatchString(identity.Account) && operationLabel(identity.Arn) {
			values := map[string]string{"account": identity.Account, "principal": identity.Arn, "region": *region}
			if *profile != "" {
				values["profile"] = *profile
			}
			outcome := "pass"
			if !complete {
				outcome = "error"
			}
			b.Contexts["aws"] = contextObservation{Kind: "aws", Values: values, CollectedAt: stamp, Source: source, Outcome: outcome}
		}
		artifact = b
	case "fleet":
		fleet.Complete = &complete
		wanted := map[string]bool{}
		for _, h := range fleet.Hosts {
			wanted[h.ID] = true
		}
		for _, item := range instances {
			if !wanted[item.InstanceID] {
				continue
			}
			values := map[string]any{}
			for k, v := range map[string]string{"instance_type": item.InstanceType, "image_id": item.ImageID, "state": item.State.Name, "vpc_id": item.VpcID, "subnet_id": item.SubnetID} {
				if operationLabel(v) {
					values[k] = v
				}
			}
			fleet.Observations = append(fleet.Observations, fleetObservation{Host: item.InstanceID, Platform: "aws-ec2", Outcome: "pass", Values: values, Source: source + "; EC2 control-plane inventory, not guest reachability", CollectedAt: stamp})
		}
		present := map[string]bool{}
		for _, o := range fleet.Observations {
			present[o.Host] = true
		}
		for id := range wanted {
			if !present[id] {
				complete = false
				gaps = append(gaps, "Required manifest instance not observed: "+id)
			}
		}
		artifact = fleet
	case "relations":
		g := relationGraph{SchemaVersion: "1", Complete: &complete, CollectedAt: stamp, Source: source + "; EC2 instance subnet/VPC/security-group attachments only; referenced resources are not independently described", Nodes: []relationNode{}, Edges: []relationEdge{}, Scopes: []relationScope{{Account: *account, Region: *region, Complete: &complete, Outcome: "pass"}}}
		nodes := map[string]relationNode{}
		edges := map[string]bool{}
		addNode := func(id, kind string) bool {
			if _, ok := nodes[id]; !ok && len(nodes) >= 10000 {
				complete = false
				return false
			}
			nodes[id] = relationNode{ID: id, Type: kind, Account: *account, Region: *region}
			return true
		}
		for _, item := range instances {
			if !addNode(item.InstanceID, "ec2-instance") {
				continue
			}
			refs := map[string]string{}
			if item.VpcID != "" {
				refs[item.VpcID] = "vpc-reference"
			}
			if item.SubnetID != "" {
				refs[item.SubnetID] = "subnet-reference"
			}
			for _, sg := range item.Groups {
				if sg.ID != "" {
					refs[sg.ID] = "security-group-reference"
				}
			}
			for ref, kind := range refs {
				if !operationLabel(ref) {
					complete = false
					gaps = append(gaps, "Invalid EC2 attachment identity")
					continue
				}
				if !addNode(ref, kind) {
					continue
				}
				key := item.InstanceID + "/" + ref
				if !edges[key] {
					g.Edges = append(g.Edges, relationEdge{From: item.InstanceID, To: ref, Kind: "observed", Source: "EC2 DescribeInstances attachment"})
					edges[key] = true
				}
			}
		}
		for _, id := range sortedRelationNodes(nodes) {
			g.Nodes = append(g.Nodes, nodes[id])
		}
		sort.Slice(g.Edges, func(i, j int) bool { a, b := g.Edges[i], g.Edges[j]; return a.From+"/"+a.To < b.From+"/"+b.To })
		if !complete {
			gaps = append(gaps, "Attachment graph incomplete; invalid identity or 10000-node limit")
			g.Scopes[0].Outcome = "error"
		}
		artifact = g
	}
	data, err := json.MarshalIndent(artifact, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if len(data) > 16<<20 {
		return fmt.Errorf("normalized collection exceeds artifact limit")
	}
	if *output == "-" {
		_, err = stdout.Write(data)
	} else {
		err = publishMarkdown(*output, data)
	}
	if err != nil {
		return err
	}
	for _, gap := range gaps {
		fmt.Fprintln(stderr, safeReportText("Incomplete: "+gap))
	}
	if !complete {
		return reportError{status: finding.StatusIncomplete}
	}
	return nil
}
