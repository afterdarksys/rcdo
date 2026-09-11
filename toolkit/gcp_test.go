package toolkit

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const gcpConfigFixture = `{"core":{"account":"operator@example.com","project":"rcdo-test"},"compute":{"zone":"us-central1-a","region":"us-central1"},"auth":{}}`
const gcpInstanceFixture = `{"id":"123456","name":"blue","zone":"https://www.googleapis.com/compute/v1/projects/rcdo-test/zones/us-central1-a","machineType":"https://www.googleapis.com/compute/v1/projects/rcdo-test/zones/us-central1-a/machineTypes/e2-small","status":"RUNNING","metadata":{"items":[{"key":"secret","value":"PRIVATE-VM-METADATA"}]},"networkInterfaces":[{"name":"nic0","network":"https://www.googleapis.com/compute/v1/projects/rcdo-test/global/networks/app","subnetwork":"https://www.googleapis.com/compute/v1/projects/rcdo-test/regions/us-central1/subnetworks/app"}]}`

func mockGCP(t *testing.T, config, items string) *int {
	t.Helper()
	original := executeReadOnly
	t.Cleanup(func() { executeReadOnly = original })
	for _, key := range []string{"CLOUDSDK_AUTH_ACCESS_TOKEN", "CLOUDSDK_AUTH_ACCESS_TOKEN_FILE", "CLOUDSDK_AUTH_CREDENTIAL_FILE_OVERRIDE", "CLOUDSDK_AUTH_IMPERSONATE_SERVICE_ACCOUNT"} {
		t.Setenv(key, "")
	}
	inventories := 0
	executeReadOnly = func(name string, args ...string) commandResult {
		if name != "gcloud" {
			t.Fatal("unexpected executable", name)
		}
		joined := strings.Join(args, " ")
		if !strings.Contains(joined, "--format=json") || !strings.Contains(joined, "--quiet") {
			t.Fatal("missing noninteractive JSON options", args)
		}
		switch {
		case strings.HasPrefix(joined, "config list --all"):
			return commandResult{stdout: []byte(config)}
		case strings.HasPrefix(joined, "auth list --filter=status:ACTIVE"):
			return commandResult{stdout: []byte(`[{"account":"operator@example.com","status":"ACTIVE"}]`)}
		case strings.HasPrefix(joined, "projects describe rcdo-test"):
			if !strings.Contains(joined, "--account=operator@example.com") || !strings.Contains(joined, "--project=rcdo-test") {
				t.Fatal("unpinned query")
			}
			return commandResult{stdout: []byte(`{"projectId":"rcdo-test","projectNumber":"123456789","lifecycleState":"ACTIVE"}`)}
		case strings.HasPrefix(joined, "compute instances list"):
			inventories++
			if !strings.Contains(joined, "--zones=us-central1-a") || !strings.Contains(joined, "--account=operator@example.com") || !strings.Contains(joined, "--project=rcdo-test") {
				t.Fatal("unpinned inventory", args)
			}
			return commandResult{stdout: []byte(items)}
		default:
			t.Fatal("unexpected command", args)
			return commandResult{err: fmt.Errorf("unexpected")}
		}
	}
	return &inventories
}
func gcpCollectArgs(kind string) []string {
	return []string{"--cloud", "gcp", "--kind", kind, "--project", "rcdo-test", "--expect-account", "operator@example.com", "--zone", "us-central1-a"}
}

func TestGCPContextAndRelationsReachReaders(t *testing.T) {
	calls := mockGCP(t, gcpConfigFixture, "["+gcpInstanceFixture+"]")
	c, out, e := execute("collect", gcpCollectArgs("context"), "")
	if c != 0 || *calls != 0 {
		t.Fatalf("%d %s %s", c, out, e)
	}
	bundle, err := readContextBundle([]byte(out))
	if err != nil || bundle.Contexts["gcp"].Values["project_number"] != "123456789" {
		t.Fatal(err, out)
	}
	c, out, e = execute("collect", gcpCollectArgs("relations"), "")
	if c != 0 || *calls != 1 || strings.Contains(out, "PRIVATE-VM-METADATA") {
		t.Fatalf("%d %s %s", c, out, e)
	}
	c, out, e = execute("resource-walk", []string{"--resource", "projects/rcdo-test/zones/us-central1-a/instances/blue", "--require-scope", "rcdo-test/us-central1-a"}, out)
	if c != 10 {
		t.Fatalf("reader %d %s %s", c, out, e)
	}
}

func TestGCPWrongContextNeverQueriesInventory(t *testing.T) {
	for _, config := range []string{strings.Replace(gcpConfigFixture, "rcdo-test", "wrong-project", 1), strings.Replace(gcpConfigFixture, "operator@example.com", "wrong@example.com", 1), strings.Replace(gcpConfigFixture, `"auth":{}`, `"auth":{"impersonate_service_account":"service@example.com"}`, 1)} {
		t.Run(config, func(t *testing.T) {
			calls := mockGCP(t, config, "[]")
			c, out, e := execute("collect", gcpCollectArgs("relations"), "")
			if c != 30 || *calls != 0 {
				t.Fatalf("%d %s %s", c, out, e)
			}
		})
	}
}

func TestGCPOverridesStopBeforeNativeCommands(t *testing.T) {
	for _, key := range []string{"CLOUDSDK_AUTH_ACCESS_TOKEN", "CLOUDSDK_API_ENDPOINT_OVERRIDES_COMPUTE"} {
		t.Run(key, func(t *testing.T) {
			mockGCP(t, gcpConfigFixture, "[]")
			t.Setenv(key, "PRIVATE-OVERRIDE")
			executeReadOnly = func(string, ...string) commandResult {
				t.Fatal("override must be rejected before native calls")
				return commandResult{}
			}
			c, out, e := execute("collect", gcpCollectArgs("relations"), "")
			if c != 30 || strings.Contains(out+e, "PRIVATE-OVERRIDE") {
				t.Fatalf("%d %s %s", c, out, e)
			}
		})
	}
}

func TestGCPEmptyInventoryAndNamedAcquisition(t *testing.T) {
	mockGCP(t, gcpConfigFixture, "[]")
	c, out, e := execute("collect", gcpCollectArgs("relations"), "")
	if c != 0 || !strings.Contains(out, `"complete": true`) {
		t.Fatalf("%d %s %s", c, out, e)
	}
	c, out, e = execute("context-acquire", []string{"--kind", "gcp", "--native", "--name", "workplace-gcp", "--project", "rcdo-test", "--expect-account", "operator@example.com"}, "")
	if c != 0 {
		t.Fatalf("%d %s %s", c, out, e)
	}
	bundle, err := readContextBundle([]byte(out))
	if err != nil || bundle.Contexts["workplace-gcp"].Kind != "gcp" {
		t.Fatal(err, out)
	}
}

func TestGCPPartialInventoryFailsClosed(t *testing.T) {
	cases := []string{"{}", "null", "[" + strings.Replace(gcpInstanceFixture, "us-central1-a", "us-east1-b", 1) + "]", "[" + gcpInstanceFixture + "," + gcpInstanceFixture + "]", "[" + strings.Replace(gcpInstanceFixture, "projects/rcdo-test/global", "projects/other-project/global", 1) + "]"}
	for _, items := range cases {
		t.Run(fmt.Sprint(len(items)), func(t *testing.T) {
			mockGCP(t, gcpConfigFixture, items)
			c, out, e := execute("collect", gcpCollectArgs("relations"), "")
			if c != 30 {
				t.Fatalf("%d %s %s", c, out, e)
			}
		})
	}
	mockGCP(t, gcpConfigFixture, "["+gcpInstanceFixture+","+strings.ReplaceAll(strings.ReplaceAll(gcpInstanceFixture, "blue", "green"), "123456", "789012")+"]")
	c, out, e := execute("collect", append(gcpCollectArgs("relations"), "--max-instances", "1"), "")
	if c != 30 || !strings.Contains(e, "limit") {
		t.Fatalf("%d %s %s", c, out, e)
	}
}

func TestGCPIdentityChangeDiscardsInventory(t *testing.T) {
	mockGCP(t, gcpConfigFixture, "["+gcpInstanceFixture+"]")
	base := executeReadOnly
	configs := 0
	executeReadOnly = func(name string, args ...string) commandResult {
		if args[0] == "config" {
			configs++
			if configs == 2 {
				return commandResult{stdout: []byte(strings.Replace(gcpConfigFixture, "rcdo-test", "another-project", 1))}
			}
		}
		return base(name, args...)
	}
	c, out, e := execute("collect", gcpCollectArgs("relations"), "")
	if c != 30 || strings.Contains(out, "gcp-compute-instance") {
		t.Fatalf("%d %s %s", c, out, e)
	}
}

func TestGCPFleetBaselineAndMissingHost(t *testing.T) {
	mockGCP(t, gcpConfigFixture, "["+gcpInstanceFixture+"]")
	complete := true
	b := fleetBundle{SchemaVersion: "1", Complete: &complete, Hosts: []fleetHost{{ID: "projects/rcdo-test/zones/us-central1-a/instances/blue", Platform: "gcp-compute", Baseline: "app"}}, Baselines: map[string]fleetBaseline{"app": {Platform: "gcp-compute", Source: "fixture", CollectedAt: time.Now().UTC().Format(time.RFC3339Nano), Values: map[string]any{"state": "RUNNING"}}}, Observations: []fleetObservation{}}
	path := filepath.Join(t.TempDir(), "fleet.json")
	save := func() {
		data, _ := json.Marshal(b)
		if e := os.WriteFile(path, data, 0600); e != nil {
			t.Fatal(e)
		}
	}
	save()
	c, out, e := execute("collect", append(gcpCollectArgs("fleet"), "--manifest", path), "")
	if c != 0 {
		t.Fatalf("%d %s %s", c, out, e)
	}
	c, out, e = execute("fleet-check", nil, out)
	if c != 0 {
		t.Fatalf("reader %d %s %s", c, out, e)
	}
	b.Hosts[0].ID = "projects/rcdo-test/zones/us-central1-a/instances/missing"
	save()
	c, out, e = execute("collect", append(gcpCollectArgs("fleet"), "--manifest", path), "")
	if c != 30 {
		t.Fatalf("%d %s %s", c, out, e)
	}
}

func TestGCPCloudContextCheck(t *testing.T) {
	mockGCP(t, gcpConfigFixture, "[]")
	args := []string{"--expect-cloud", "gcp", "--expect-project", "rcdo-test", "--expect-account", "operator@example.com", "--expect-region", "us-central1", "--expect-zone", "us-central1-a"}
	c, out, e := execute("cloud-context-check", append(args, "--collect"), "")
	if c != 0 {
		t.Fatalf("%d %s %s", c, out, e)
	}
	c, out, e = execute("cloud-context-check", args, `{"cloud":"gcp","project":"other-project","account":"operator@example.com","region":"us-central1","zone":"us-central1-a"}`)
	if c != 20 || strings.Contains(out, "other-project") {
		t.Fatalf("%d %s %s", c, out, e)
	}
}

func TestGCPCommandPreviews(t *testing.T) {
	for resource, extra := range map[string]string{"network": ``, "subnet": `,"region":"us-central1","network":"app","range":"10.0.0.0/24"`, "bucket": `,"location":"US"`, "service-account": `,"display_name":"Application"`} {
		for _, shell := range []string{"posix", "powershell"} {
			input := `{"project":"rcdo-test","account":"operator@example.com","resource":"` + resource + `","name":"app-example"` + extra + `}`
			c, out, e := execute("command-gen", []string{"--to", "gcp", "--shell", shell, "--format", "json"}, input)
			var r commandRecipe
			if c != 0 || json.Unmarshal([]byte(out), &r) != nil || r.Executed || !r.Mutating || r.Argv[0] != "gcloud" || !strings.Contains(r.Command, "--project=rcdo-test") {
				t.Fatalf("%d %s %s", c, out, e)
			}
		}
	}
	for _, input := range []string{`{"project":"rcdo-test","account":"operator@example.com","resource":"network","name":"app","ignored":"value"}`, `{"project":"rcdo-test","account":"operator@example.com","resource":"network","name":"--other-project"}`, `{"resource":"bucket","name":"app-example"}`} {
		c, out, e := execute("command-gen", []string{"--to", "gcp"}, input)
		if c != 2 {
			t.Fatalf("%d %s %s", c, out, e)
		}
	}
}
