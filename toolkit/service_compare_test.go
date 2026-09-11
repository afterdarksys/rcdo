package toolkit

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"git-tools/finding"
)

func serviceFixture() serviceDeploymentBundle {
	complete := true
	b := serviceDeploymentBundle{SchemaVersion: "1"}
	for _, s := range builtInServiceCatalog().Families[0].Services {
		b.Deployments = append(b.Deployments, serviceDeployment{Name: s.Cloud, Cloud: s.Cloud, Account: "fixture-account", Location: "fixture-location", Source: "synthetic fixture", CollectedAt: time.Now().UTC().Format(time.RFC3339Nano), Complete: &complete, Resources: []serviceResource{{Key: "uploads", Service: s.ID, ID: "PRIVATE-RESOURCE-ID", Properties: map[string]any{"public_access": false, "versioning": true, "customer_managed_key": true}}}})
	}
	return b
}

func TestServiceFourCloudPairsAndSelection(t *testing.T) {
	b := serviceFixture()
	code, out, e := execute("service-compare", nil, jsonFixture(b))
	if code != 0 || strings.Count(out, "Pair ") != 6 || strings.Contains(out, "PRIVATE-RESOURCE-ID") {
		t.Fatalf("%d %s %s", code, out, e)
	}
	b.Deployments[0].Resources[0].Properties["versioning"] = false
	code, out, e = execute("service-compare", []string{"--format", "json"}, jsonFixture(b))
	var r finding.Report
	if code != 10 || json.Unmarshal([]byte(out), &r) != nil || len(r.Findings) != 3 {
		t.Fatalf("%d %s %s", code, out, e)
	}
	code, out, e = execute("service-compare", []string{"--deployments", "gcp,oci"}, jsonFixture(b))
	if code != 0 || strings.Count(out, "Pair ") != 1 {
		t.Fatalf("%d %s %s", code, out, e)
	}
	for _, selection := range []string{"aws", "aws,aws", "aws,missing", "aws,"} {
		code, _, _ = execute("service-compare", []string{"--deployments", selection}, jsonFixture(b))
		if code != 2 {
			t.Fatal(selection, code)
		}
	}
}

func TestServiceUnknownEvidenceNeverMatches(t *testing.T) {
	cases := map[string]func(*serviceDeploymentBundle){
		"missing field": func(b *serviceDeploymentBundle) { delete(b.Deployments[0].Resources[0].Properties, "versioning") },
		"null field":    func(b *serviceDeploymentBundle) { b.Deployments[0].Resources[0].Properties["versioning"] = nil },
		"wrong type":    func(b *serviceDeploymentBundle) { b.Deployments[0].Resources[0].Properties["versioning"] = "true" },
		"unmapped field": func(b *serviceDeploymentBundle) {
			b.Deployments[0].Resources[0].Properties["provider_detail"] = "PRIVATE-VALUE"
		},
		"unknown service": func(b *serviceDeploymentBundle) { b.Deployments[0].Resources[0].Service = "unknown" },
		"unknown cloud":   func(b *serviceDeploymentBundle) { b.Deployments[0].Cloud = "unknown" },
		"coverage":        func(b *serviceDeploymentBundle) { b.Deployments[0].Complete = nil },
		"source":          func(b *serviceDeploymentBundle) { b.Deployments[0].Source = "" },
		"stale": func(b *serviceDeploymentBundle) {
			b.Deployments[0].CollectedAt = time.Now().Add(-time.Hour).Format(time.RFC3339)
		},
		"future": func(b *serviceDeploymentBundle) {
			b.Deployments[0].CollectedAt = time.Now().Add(time.Hour).Format(time.RFC3339)
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			b := serviceFixture()
			mutate(&b)
			code, out, e := execute("service-compare", nil, jsonFixture(b))
			if code != 30 || strings.Contains(out, "aws vs azure; role uploads; family object-storage: all") || strings.Contains(out+e, "PRIVATE-VALUE") {
				t.Fatalf("%d %s %s", code, out, e)
			}
		})
	}
}

func TestServiceMissingResourceAndDifferentFamilies(t *testing.T) {
	b := serviceFixture()
	b.Deployments[0].Resources = []serviceResource{}
	code, out, e := execute("service-compare", nil, jsonFixture(b))
	if code != 10 || !strings.Contains(out, "missing") {
		t.Fatalf("%d %s %s", code, out, e)
	}
	b.Deployments[0].Complete = nil
	code, out, e = execute("service-compare", []string{"--deployments", "aws,gcp"}, jsonFixture(b))
	if code != 30 || strings.Contains(out, "absent from") {
		t.Fatalf("%d %s %s", code, out, e)
	}
	b = serviceFixture()
	b.Deployments[0].Resources[0].Service = "ebs"
	b.Deployments[0].Resources[0].Properties = map[string]any{"size_gib": 100, "encrypted": true}
	code, out, e = execute("service-compare", nil, jsonFixture(b))
	if code != 10 || !strings.Contains(out, "different service families") {
		t.Fatalf("%d %s %s", code, out, e)
	}
	for i := range b.Deployments {
		b.Deployments[i].Resources = []serviceResource{}
	}
	code, out, e = execute("service-compare", nil, jsonFixture(b))
	if code != 30 {
		t.Fatalf("%d %s %s", code, out, e)
	}
}

func TestServiceCatalogExportValidationAndCustomComparison(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "maps.json")
	code, out, e := execute("service-map", []string{"--output", path}, "")
	if code != 0 {
		t.Fatalf("%d %s %s", code, out, e)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal(info, err)
	}
	code, _, _ = execute("service-map", []string{"--output", path}, "")
	if code != 2 {
		t.Fatal("overwrite allowed", code)
	}
	code, out, e = execute("service-map", []string{"--input", path, "--format", "json"}, "")
	var catalog serviceCatalog
	if code != 0 || json.Unmarshal([]byte(out), &catalog) != nil || len(catalog.Families) != 2 {
		t.Fatalf("%d %s %s", code, out, e)
	}
	custom := serviceCatalog{SchemaVersion: "1", Families: []serviceFamily{{ID: "queue", Description: "Custom queue contract", Services: []mappedService{{"one", "queue", "Queue One"}, {"two", "queue", "Queue Two"}}, Fields: map[string]serviceField{"label": {Type: "string", Description: "Logical label"}, "retention": {Type: "number", Unit: "seconds", Description: "Retention duration"}}}}}
	customPath := writeFixture(t, dir, "custom.json", jsonFixture(custom))
	b := serviceFixture()
	b.Deployments = b.Deployments[:2]
	for i, cloud := range []string{"one", "two"} {
		b.Deployments[i].Cloud = cloud
		b.Deployments[i].Resources[0].Service = "queue"
		b.Deployments[i].Resources[0].Properties = map[string]any{"label": "PRIVATE-FIRST", "retention": json.Number("9007199254740993")}
	}
	b.Deployments[1].Resources[0].Properties["label"] = "PRIVATE-SECOND"
	args := []string{"--maps", customPath}
	code, out, e = execute("service-compare", args, jsonFixture(b))
	if code != 10 || strings.Contains(out, "PRIVATE-FIRST") {
		t.Fatalf("%d %s %s", code, out, e)
	}
	code, out, e = execute("service-compare", append(args, "--values"), jsonFixture(b))
	if code != 10 || !strings.Contains(out, "PRIVATE-FIRST") || !strings.Contains(out, "PRIVATE-SECOND") {
		t.Fatalf("%d %s %s", code, out, e)
	}
	b.Deployments[1].Resources[0].Properties["label"] = "PRIVATE-FIRST"
	b.Deployments[1].Resources[0].Properties["retention"] = json.Number("9007199254740993.0")
	code, out, e = execute("service-compare", args, jsonFixture(b))
	if code != 0 {
		t.Fatalf("equal decimals: %d %s %s", code, out, e)
	}
	b.Deployments[1].Resources[0].Properties["retention"] = json.Number("9007199254740992")
	code, out, e = execute("service-compare", args, jsonFixture(b))
	if code != 10 {
		t.Fatalf("rounded difference: %d %s %s", code, out, e)
	}
	b.Deployments[1].Resources[0].Properties["retention"] = json.Number("1e9999999")
	code, out, e = execute("service-compare", args, jsonFixture(b))
	if code != 30 {
		t.Fatalf("huge exponent: %d %s %s", code, out, e)
	}
}

func TestServiceInvalidCatalogsAndBundles(t *testing.T) {
	for _, raw := range []string{`{}`, `{"schema_version":"1"}`, `{"schema_version":"1","families":[]}`, `{"schema_version":"1","schema_version":"1","families":[]}`} {
		code, _, _ := execute("service-map", []string{"--input", "-"}, raw)
		if code != 2 {
			t.Fatal(raw, code)
		}
	}
	c := builtInServiceCatalog()
	c.Families[1].Services = append(c.Families[1].Services, c.Families[0].Services[0])
	if _, err := validateServiceCatalog(c); err == nil {
		t.Fatal("ambiguous mapping accepted")
	}
	c = builtInServiceCatalog()
	f := c.Families[1].Fields["size_gib"]
	f.Unit = ""
	c.Families[1].Fields["size_gib"] = f
	if _, err := validateServiceCatalog(c); err == nil {
		t.Fatal("unitless number accepted")
	}
	b := serviceFixture()
	b.Deployments[0].Resources = append(b.Deployments[0].Resources, b.Deployments[0].Resources[0])
	code, _, _ := execute("service-compare", nil, jsonFixture(b))
	if code != 2 {
		t.Fatal("ambiguous role accepted", code)
	}
	b = serviceFixture()
	b.Deployments = append(b.Deployments, b.Deployments[0])
	code, _, _ = execute("service-compare", nil, jsonFixture(b))
	if code != 2 {
		t.Fatal("duplicate deployment accepted", code)
	}
	code, _, _ = execute("service-compare", nil, strings.Replace(jsonFixture(serviceFixture()), `"schema_version":"1"`, `"schema_version":"1","unrecognized":true`, 1))
	if code != 2 {
		t.Fatal("unknown schema field accepted", code)
	}
}

func TestServiceConfiguredCatalogAndProvenance(t *testing.T) {
	dir := t.TempDir()
	maps := writeFixture(t, dir, "maps.json", jsonFixture(builtInServiceCatalog()))
	input := writeFixture(t, dir, "deployments.json", jsonFixture(serviceFixture()))
	c := defaultAppConfig()
	c.Commands["service-compare"] = map[string]any{"maps": maps, "format": "json"}
	config := writeAppConfigFixture(t, c)
	code, out, e := execute("service-compare", []string{"--config-file", config, "--input", input, "--change-id", "compare-test", "--commit", strings.Repeat("a", 40), "--environment", "test"}, "")
	var r finding.Report
	if code != 0 || json.Unmarshal([]byte(out), &r) != nil || r.Provenance == nil || len(r.Provenance.Artifacts) != 2 {
		t.Fatalf("%d %s %s", code, out, e)
	}
	if err := os.WriteFile(maps, []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	if checkProvenanceArtifacts(r.Provenance) == nil {
		t.Fatal("catalog changes did not invalidate provenance")
	}
	for _, command := range []string{"service-map", "service-compare"} {
		code, out, e = execute(command, []string{"--help"}, "")
		if code != 0 {
			t.Fatalf("%d %s %s", code, out, e)
		}
		for key := range configurableFlags[command] {
			if !strings.Contains(out+e, "Option: --"+key+"\n") {
				t.Fatal("missing configurable flag", command, key)
			}
		}
	}
}
