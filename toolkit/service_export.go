package toolkit

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"time"

	"git-tools/finding"
)

// service-export maps a narrow, already-redacted provider observation into one
// service-comparison deployment. It does not call a cloud and it does not
// convert units it cannot prove. A missing control stays omitted and the
// deployment is incomplete. False is never a substitute for unknown.

type serviceExportInput struct {
	Schema     string         `json:"schema"`
	Deployment string         `json:"deployment"`
	Cloud      string         `json:"cloud"`
	Service    string         `json:"service"`
	Key        string         `json:"key"`
	ID         string         `json:"id"`
	Account    string         `json:"account"`
	Location   string         `json:"location"`
	Source     string         `json:"source"`
	Collected  string         `json:"collected_at"`
	Observed   map[string]any `json:"observed"`
}

func runServiceExport(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	_, options, err := parseFlags("service-export", args, stderr, func(fs *flag.FlagSet) *commonOptions {
		var options commonOptions
		addCommonFlags(fs, &options)
		return &options
	})
	if err != nil {
		return err
	}
	if !oneOf(options.format, "text", "json") {
		return fmt.Errorf("service-export supports text or json")
	}
	data, err := readInput(options.input, stdin)
	if err != nil {
		return err
	}
	var input serviceExportInput
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&input); err != nil || input.Schema != "rcdo/service-export/v1" {
		return fmt.Errorf("service export requires schema rcdo/service-export/v1")
	}
	if !serviceKeyPattern.MatchString(input.Deployment) || !serviceKeyPattern.MatchString(input.Cloud) || !serviceKeyPattern.MatchString(input.Service) || !serviceKeyPattern.MatchString(input.Key) || !operationLabel(input.ID) || !operationLabel(input.Account) || !operationLabel(input.Location) || !operationLabel(input.Source) {
		return fmt.Errorf("service export identity fields are missing or invalid")
	}
	if _, err := time.Parse(time.RFC3339Nano, input.Collected); err != nil {
		return fmt.Errorf("collected_at must be RFC3339")
	}
	properties, notes, err := exportServiceProperties(input.Cloud, input.Service, input.Observed)
	if err != nil {
		return err
	}
	complete := len(notes) == 0
	deployment := serviceDeployment{
		Name: input.Deployment, Cloud: input.Cloud, Account: input.Account, Location: input.Location,
		Source: input.Source, CollectedAt: input.Collected, Complete: &complete,
		Resources: []serviceResource{{Key: input.Key, Service: input.Service, ID: input.ID, Properties: properties}},
	}
	if options.format == "json" {
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		encoder.SetEscapeHTML(false)
		if err := encoder.Encode(deployment); err != nil {
			return err
		}
		if !complete {
			return reportError{status: finding.StatusIncomplete}
		}
		return nil
	}
	report := finding.Report{CompletedChecks: []string{"Provider values were mapped into the service-comparison contract. Matching fields do not establish equivalent services. No cloud command was run."}}
	for name, value := range properties {
		report.CompletedChecks = append(report.CompletedChecks, fmt.Sprintf("Mapped %s: %v", name, value))
	}
	report.IncompleteChecks = append(report.IncompleteChecks, notes...)
	return emitReportOptions(stdout, options, report)
}

func exportServiceProperties(cloud, service string, observed map[string]any) (map[string]any, []string, error) {
	if observed == nil {
		return nil, nil, fmt.Errorf("observed object is required")
	}
	switch cloud + "/" + service {
	case "aws/s3":
		return exportS3(observed)
	case "azure/blob-storage":
		return exportBlob(observed)
	case "gcp/cloud-storage":
		return exportGCS(observed)
	case "aws/ebs", "azure/managed-disks":
		return exportBlock(observed)
	case "gcp/persistent-disk":
		if _, ok := observed["size_gb"]; ok {
			return map[string]any{}, []string{"GCP size_gb is decimal gigabytes. size_gib was not inferred."}, nil
		}
		return exportBlock(observed)
	default:
		return nil, nil, fmt.Errorf("unsupported export %s/%s", cloud, service)
	}
}

func exportS3(observed map[string]any) (map[string]any, []string, error) {
	properties := map[string]any{}
	notes := []string{}
	blocks := []string{"block_public_acls", "ignore_public_acls", "block_public_policy", "restrict_public_buckets"}
	anonymous, anonymousOK := observedBool(observed, "anonymous_read")
	allBlocks, blocksOK := true, true
	for _, name := range blocks {
		value, ok := observedBool(observed, name)
		if !ok {
			blocksOK = false
			continue
		}
		allBlocks = allBlocks && value
	}
	if anonymousOK && blocksOK {
		properties["public_access"] = anonymous && !allBlocks
	} else {
		notes = append(notes, "public_access was withheld; anonymous read or a block-public-access flag was missing")
	}
	switch observed["versioning"] {
	case "Enabled":
		properties["versioning"] = true
	case "Suspended":
		properties["versioning"] = false
	default:
		notes = append(notes, "versioning was withheld; expected Enabled or Suspended")
	}
	switch observed["encryption"] {
	case "aws:kms":
		properties["customer_managed_key"] = true
	case "AES256":
		properties["customer_managed_key"] = false
	default:
		notes = append(notes, "customer_managed_key was withheld; expected aws:kms or AES256")
	}
	return properties, notes, nil
}

func exportBlob(observed map[string]any) (map[string]any, []string, error) {
	properties := map[string]any{}
	notes := []string{}
	switch observed["public_access"] {
	case "private":
		properties["public_access"] = false
	case "blob", "container":
		properties["public_access"] = true
	default:
		notes = append(notes, "public_access was withheld; expected private, blob, or container")
	}
	if value, ok := observedBool(observed, "versioning"); ok {
		properties["versioning"] = value
	} else {
		notes = append(notes, "versioning was withheld")
	}
	switch observed["encryption"] {
	case "customer":
		properties["customer_managed_key"] = true
	case "microsoft":
		properties["customer_managed_key"] = false
	default:
		notes = append(notes, "customer_managed_key was withheld; expected customer or microsoft")
	}
	return properties, notes, nil
}

func exportGCS(observed map[string]any) (map[string]any, []string, error) {
	properties := map[string]any{}
	notes := []string{}
	switch observed["public_access_prevention"] {
	case "enforced":
		properties["public_access"] = false
	default:
		notes = append(notes, "public_access was withheld; only public access prevention enforced is a known false")
	}
	if value, ok := observedBool(observed, "versioning"); ok {
		properties["versioning"] = value
	} else {
		notes = append(notes, "versioning was withheld")
	}
	if value, ok := observedBool(observed, "customer_managed_key"); ok {
		properties["customer_managed_key"] = value
	} else {
		notes = append(notes, "customer_managed_key was withheld")
	}
	return properties, notes, nil
}

func exportBlock(observed map[string]any) (map[string]any, []string, error) {
	properties := map[string]any{}
	notes := []string{}
	if raw, ok := observed["size_bytes"]; ok {
		number, ok := raw.(json.Number)
		if !ok {
			return nil, nil, fmt.Errorf("size_bytes must be an integer")
		}
		value, err := number.Int64()
		if err != nil || value <= 0 || value%(1<<30) != 0 {
			notes = append(notes, "size_gib was withheld; size_bytes was missing or not a positive whole number of gibibytes")
		} else {
			properties["size_gib"] = json.Number(fmt.Sprintf("%d", value/(1<<30)))
		}
	} else if raw, ok := observed["size_gib"]; ok {
		if _, ok := serviceNumber(raw); ok {
			properties["size_gib"] = raw
		} else {
			notes = append(notes, "size_gib was withheld; the supplied number was not usable")
		}
	} else {
		notes = append(notes, "size_gib was withheld")
	}
	if value, ok := observedBool(observed, "encrypted"); ok {
		properties["encrypted"] = value
	} else {
		notes = append(notes, "encrypted was withheld")
	}
	return properties, notes, nil
}

func observedBool(observed map[string]any, name string) (bool, bool) {
	value, ok := observed[name].(bool)
	return value, ok
}
