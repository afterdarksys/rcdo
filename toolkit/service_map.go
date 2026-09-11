package toolkit

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"regexp"
	"sort"

	"git-tools/finding"
)

// A family asserts comparable purpose and explicit normalized field semantics,
// not interchangeable APIs, operational behavior, pricing or service guarantees.
type serviceCatalog struct {
	SchemaVersion string          `json:"schema_version"`
	Families      []serviceFamily `json:"families"`
}
type serviceFamily struct {
	ID          string                  `json:"id"`
	Description string                  `json:"description"`
	Services    []mappedService         `json:"services"`
	Fields      map[string]serviceField `json:"fields"`
}
type mappedService struct {
	Cloud string `json:"cloud"`
	ID    string `json:"id"`
	Name  string `json:"name"`
}
type serviceField struct {
	Type        string `json:"type"`
	Unit        string `json:"unit,omitempty"`
	Description string `json:"description"`
}

var serviceKeyPattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)

func sortedServiceKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func builtInServiceCatalog() serviceCatalog {
	return serviceCatalog{SchemaVersion: "1", Families: []serviceFamily{
		{ID: "object-storage", Description: "Object storage; compare normalized controls only, not API or durability parity.",
			Services: []mappedService{{"aws", "s3", "Amazon S3"}, {"azure", "blob-storage", "Azure Blob Storage"}, {"gcp", "cloud-storage", "Google Cloud Storage"}, {"oci", "object-storage", "Oracle Object Storage"}},
			Fields: map[string]serviceField{
				"public_access":        {Type: "boolean", Description: "Anonymous reads are effectively permitted for this logical resource, including inherited policy; unknown if not established."},
				"versioning":           {Type: "boolean", Description: "New writes retain prior object versions for this resource; this does not assert equivalent restore or retention behavior."},
				"customer_managed_key": {Type: "boolean", Description: "Default encryption for new writes uses a customer-managed key; existing object encryption is outside this field."},
			}},
		{ID: "block-storage", Description: "Durable VM block volumes; size and encryption do not establish performance or availability parity.",
			Services: []mappedService{{"aws", "ebs", "Amazon EBS"}, {"azure", "managed-disks", "Azure Managed Disks"}, {"gcp", "persistent-disk", "Google Persistent Disk"}, {"gcp", "hyperdisk", "Google Hyperdisk"}, {"oci", "block-volume", "Oracle Block Volumes"}},
			Fields: map[string]serviceField{
				"size_gib":  {Type: "number", Unit: "GiB", Description: "Provisioned volume capacity in gibibytes (2^30 bytes), not used space."},
				"encrypted": {Type: "boolean", Description: "The volume is encrypted at rest; key ownership and rotation are separate controls."},
			}},
	}}
}

func validateServiceCatalog(c serviceCatalog) (map[string]serviceFamily, error) {
	if c.SchemaVersion != "1" || len(c.Families) == 0 || len(c.Families) > 64 {
		return nil, fmt.Errorf("service catalog requires version 1 and 1..64 families")
	}
	index, families := map[string]serviceFamily{}, map[string]bool{}
	for _, f := range c.Families {
		if !serviceKeyPattern.MatchString(f.ID) || families[f.ID] || !operationLabel(f.Description) || len(f.Services) < 2 || len(f.Services) > 32 || len(f.Fields) == 0 || len(f.Fields) > 32 {
			return nil, fmt.Errorf("invalid or duplicate service family; require description, 2..32 services and 1..32 fields")
		}
		families[f.ID] = true
		for name, field := range f.Fields {
			if !serviceKeyPattern.MatchString(name) || !oneOf(field.Type, "boolean", "string", "number") || !operationLabel(field.Description) || (field.Type == "number" && !operationLabel(field.Unit)) || (field.Type != "number" && field.Unit != "") {
				return nil, fmt.Errorf("invalid field contract in family %s; numbers require an explicit unit", f.ID)
			}
		}
		for _, s := range f.Services {
			if !serviceKeyPattern.MatchString(s.Cloud) || !serviceKeyPattern.MatchString(s.ID) || !operationLabel(s.Name) {
				return nil, fmt.Errorf("service requires cloud, ID and display name")
			}
			key := s.Cloud + "/" + s.ID
			if _, exists := index[key]; exists {
				return nil, fmt.Errorf("ambiguous service mapping: %s", key)
			}
			index[key] = f
		}
	}
	return index, nil
}

func loadServiceCatalog(path string, stdin io.Reader) (serviceCatalog, []byte, error) {
	c := builtInServiceCatalog()
	if path != "" {
		c = serviceCatalog{}
		raw, err := readInput(path, stdin)
		if err != nil {
			return c, nil, err
		}
		if len(raw) > 1<<20 || workflowJSON(raw, &c) != nil {
			return c, nil, fmt.Errorf("service catalog must be strict versioned JSON, at most 1 MiB")
		}
		if _, err := validateServiceCatalog(c); err != nil {
			return c, nil, err
		}
		return c, raw, nil
	}
	raw, err := json.Marshal(c)
	return c, raw, err
}

func runServiceMap(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("service-map", flag.ContinueOnError)
	fs.SetOutput(stderr)
	input := fs.String("input", "", "custom catalog JSON; omitted uses built-in storage mappings")
	output := fs.String("output", "", "export validated catalog JSON to a new private file")
	format := fs.String("format", "text", "text or json")
	width := fs.Int("width", finding.DefaultTextWidth, "maximum text line width; minimum 40")
	setAccessibleUsage(fs, "service-map", stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 || !oneOf(*format, "text", "json") || *width < 40 {
		return fmt.Errorf("invalid arguments, format or width")
	}
	c, _, err := loadServiceCatalog(*input, stdin)
	if err != nil {
		return err
	}
	// Stable rendering also makes exported custom catalogs reviewable in Git.
	sort.Slice(c.Families, func(i, j int) bool { return c.Families[i].ID < c.Families[j].ID })
	for i := range c.Families {
		sort.Slice(c.Families[i].Services, func(a, b int) bool {
			x, y := c.Families[i].Services[a], c.Families[i].Services[b]
			return x.Cloud+"/"+x.ID < y.Cloud+"/"+y.ID
		})
	}
	if *output != "" || *format == "json" {
		raw, err := json.MarshalIndent(c, "", "  ")
		if err != nil {
			return err
		}
		raw = append(raw, '\n')
		if *output != "" {
			return publishMarkdown(*output, raw)
		}
		_, err = stdout.Write(raw)
		return err
	}
	r := finding.Report{CompletedChecks: []string{"Like-service families describe comparable purpose, not interchangeable services. All listed fields require normalized evidence. Catalog membership does not enable native cloud collection."}}
	for _, f := range c.Families {
		r.CompletedChecks = append(r.CompletedChecks, "Family "+f.ID+": "+safeReportText(f.Description))
		for _, s := range f.Services {
			r.CompletedChecks = append(r.CompletedChecks, "Family "+f.ID+"; service "+s.Cloud+"/"+s.ID+": "+safeReportText(s.Name))
		}
		for _, name := range sortedServiceKeys(f.Fields) {
			v := f.Fields[name]
			r.CompletedChecks = append(r.CompletedChecks, "Family "+f.ID+"; required field "+name+"; type "+v.Type+"; unit "+emptyValue(v.Unit)+". "+safeReportText(v.Description))
		}
	}
	return emitReport(stdout, "text", *width, r)
}
