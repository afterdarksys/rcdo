package toolkit

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestShiftSpeaksOneBlockerAndRefusesACleanReportWithoutIdentity(t *testing.T) {
	dir := t.TempDir()
	clean := filepath.Join(dir, "plan.json")
	body := `{"schema_version":"1","status":"clean","findings":[],"completed_checks":["plan read"],"incomplete_checks":[]}`
	if err := os.WriteFile(clean, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	code, out, err := execute("shift", []string{"--report", clean, "--layout", "speech", "--width", "40"}, "")
	if code != 30 || !strings.Contains(out, "Identity: not in the supplied reports.") || !strings.Contains(out, "Deployment proven: no.") || err != "" {
		t.Fatalf("%d %s %s", code, out, err)
	}
	for _, line := range strings.Split(strings.TrimSuffix(out, "\n"), "\n") {
		if len([]rune(line)) > 40 {
			t.Fatalf("line wider than 40: %s", line)
		}
	}

	identity := filepath.Join(dir, "context.json")
	blocked := filepath.Join(dir, "policy.json")
	idBody := `{"schema_version":"1","status":"clean","findings":[],"completed_checks":["identity label: cloud azure; subscription aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee; principal operator@example.com"],"incomplete_checks":[]}`
	blockBody := `{"schema_version":"1","status":"blocked","findings":[{"id":"POLICY-1","severity":"high","title":"Spacelift policy denied the run","resource":"stack payments","action":"policy","environment":"staging","reason":"denied","evidence":["policy deny"],"confidence":"high","remediation":"Read the policy decision before asking for approval."}],"completed_checks":["policy read"],"incomplete_checks":["play limit was not evaluated"]}`
	if err := os.WriteFile(identity, []byte(idBody), 0o600); err != nil || os.WriteFile(blocked, []byte(blockBody), 0o600) != nil {
		t.Fatal(err)
	}
	code, out, err = execute("shift", []string{"--report", identity, "--report", blocked, "--layout", "speech", "--format", "text", "--change-id", "CHG-42"}, "")
	if code != 30 || !strings.Contains(out, "subscription aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee") || !strings.Contains(out, "Spacelift policy denied the run") || !strings.Contains(out, "play limit was not evaluated") || !strings.Contains(out, "report-read") || err != "" {
		t.Fatalf("%d %s %s", code, out, err)
	}
	code, out, err = execute("shift", []string{"--report", identity, "--report", blocked, "--format", "ticket", "--change-id", "CHG-42", "--width", "72"}, "")
	if code != 30 || !strings.Contains(out, "Change: CHG-42") || !strings.Contains(out, "not approval to deploy") || !strings.Contains(out, "SHA-256") || err != "" {
		t.Fatalf("ticket %d %s %s", code, out, err)
	}
	code, out, err = execute("shift", []string{"--report", identity, "--format", "json"}, "")
	if code != 0 || !strings.Contains(out, `"deployment_proven": false`) || !strings.Contains(out, `"schema": "rcdo/shift/v1"`) || err != "" {
		t.Fatalf("json %d %s %s", code, out, err)
	}
}

func TestShiftRejectsAReportWhoseStatusLies(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lie.json")
	body := `{"schema_version":"1","status":"clean","findings":[{"id":"LIE-1","severity":"critical","title":"Wrong account","resource":"account","action":"verify target","environment":"staging","reason":"mismatch","evidence":["expected other"],"confidence":"high","remediation":"Recollect identity."}],"completed_checks":["identity label: cloud aws; account 111111111111; principal role/test"],"incomplete_checks":[]}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	code, out, err := execute("shift", []string{"--report", path, "--layout", "braille", "--width", "40"}, "")
	spoken := strings.Join(strings.Fields(out), " ")
	if code != 30 || !strings.Contains(spoken, "report status does not match its findings") || !strings.Contains(spoken, "Wrong account") || err != "" {
		t.Fatalf("%d %s %s", code, out, err)
	}
	for _, line := range strings.Split(strings.TrimSuffix(out, "\n"), "\n") {
		if len([]rune(line)) > 40 {
			t.Fatalf("line wider than 40: %q", line)
		}
	}
}

func TestAzureIdentityAcquireWithholdsDiagnostics(t *testing.T) {
	subscription := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	tenant := "11111111-2222-3333-4444-555555555555"
	good := fmt.Sprintf(`{"id":"%s","tenantId":"%s","name":"payments","state":"Enabled","principal":"operator@example.com"}`, subscription, tenant)
	args := []string{"--kind", "azure", "--native", "--name", "work", "--subscription", subscription, "--expect-tenant", tenant}
	calls := 0
	original := executeReadOnly
	t.Cleanup(func() { executeReadOnly = original })
	executeReadOnly = func(name string, argv ...string) commandResult {
		calls++
		if name != "az" || !strings.Contains(strings.Join(argv, " "), subscription) {
			t.Fatalf("unexpected command %s %v", name, argv)
		}
		return commandResult{stdout: []byte(good)}
	}
	code, out, err := execute("context-acquire", args, "")
	if code != 0 || calls != 2 || !strings.Contains(out, `"kind": "azure"`) || !strings.Contains(out, `"subscription": "`+subscription+`"`) || err != "" {
		t.Fatalf("%d calls=%d %s %s", code, calls, out, err)
	}

	executeReadOnly = func(string, ...string) commandResult {
		t.Fatal("invalid GUID must not call az")
		return commandResult{}
	}
	code, out, err = execute("context-acquire", []string{"--kind", "azure", "--native", "--name", "work", "--subscription", "nope", "--expect-tenant", tenant}, "")
	if code != 30 || strings.Contains(out+err, "nope") && strings.Contains(err, "az ") {
		t.Fatalf("%d %s %s", code, out, err)
	}

	executeReadOnly = func(string, ...string) commandResult {
		return commandResult{err: fmt.Errorf("login failed"), stderr: "Bearer TOKEN-SHOULD-NOT-LEAK"}
	}
	code, out, err = execute("context-acquire", args, "")
	if code != 30 || strings.Contains(out+err, "TOKEN-SHOULD-NOT-LEAK") || strings.Contains(out+err, "Bearer") {
		t.Fatalf("leaked diagnostics %d %s %s", code, out, err)
	}
}

func TestContextAcceptsAzureSubscriptionAndGCPProject(t *testing.T) {
	subscription := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	for _, tc := range []struct {
		cloud, account string
		code           int
	}{
		{"azure", subscription, 0},
		{"azure", "bbbbbbbb-bbbb-cccc-dddd-eeeeeeeeeeee", 20},
		{"gcp", "rcdo-test", 0},
		{"gcp", "other-project", 20},
	} {
		path := filepath.Join(t.TempDir(), "expect.yaml")
		expectedAccount := subscription
		if tc.cloud == "gcp" {
			expectedAccount = "rcdo-test"
		}
		body := "schema_version: '1'\nname: payments\nenvironment: staging\ncloud: " + tc.cloud + "\nprofile: work\nregion: test-region\naccount: '" + expectedAccount + "'\n"
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		raw := fmt.Sprintf(`{"schema":"missing-utils/contextsnap/v1","source":"provided","cloud":%q,"profile":"work","region":"test-region","account":%q,"principal":"operator@example.com","collected_at":%q,"collector_version":"test","outcome":"pass","diagnostics":[]}`, tc.cloud, tc.account, time.Now().UTC().Format(time.RFC3339Nano))
		code, out, err := execute("context", []string{"--expect", path, "--format", "text", "--width", "40"}, raw)
		if code != tc.code || err != "" || (tc.code == 0 && !strings.Contains(out, "identity label:")) {
			t.Fatalf("%s %s code %d %s %s", tc.cloud, tc.account, code, out, err)
		}
	}
	path := filepath.Join(t.TempDir(), "bad.yaml")
	if err := os.WriteFile(path, []byte("schema_version: '1'\nname: payments\nenvironment: staging\ncloud: azure\nprofile: work\nregion: test-region\naccount: 'not-a-guid'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, _, err := execute("context", []string{"--expect", path}, `{}`)
	if code != 2 || !strings.Contains(err, "subscription GUID") {
		t.Fatalf("%d %s", code, err)
	}
}

func TestPermissionDiffSpeaksBroadAllow(t *testing.T) {
	dir := t.TempDir()
	before := filepath.Join(dir, "before.json")
	after := filepath.Join(dir, "after.json")
	if err := os.WriteFile(before, []byte(`{"Version":"2012-10-17","Statement":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(after, []byte(`{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":["iam:PassRole"],"Resource":"arn:aws:iam::123456789012:role/deploy"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	code, out, err := execute("permission-diff", []string{"--before", before, "--after", after, "--kind", "aws", "--format", "text", "--width", "80"}, "")
	if code != 20 || !strings.Contains(out, "pass a role or assume another identity") || err != "" {
		t.Fatalf("passrole %d %s %s", code, out, err)
	}
	if err := os.WriteFile(after, []byte(`{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"*","Resource":"*"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	code, out, err = execute("permission-diff", []string{"--before", before, "--after", after, "--kind", "aws", "--format", "text"}, "")
	if code != 20 || !strings.Contains(out, "does not prove the identity can use that access") || err != "" {
		t.Fatalf("star %d %s %s", code, out, err)
	}
}

func TestSeeReadsMetadataAndWithholdsExtractorNoise(t *testing.T) {
	dir := t.TempDir()
	image := filepath.Join(dir, "console.png")
	if err := os.WriteFile(image, []byte("png"), 0o600); err != nil {
		t.Fatal(err)
	}
	original := executeReadOnly
	t.Cleanup(func() { executeReadOnly = original })
	executeReadOnly = func(name string, args ...string) commandResult {
		if name != "imgsee-test" || len(args) < 2 || args[1] != "info" {
			t.Fatalf("args %s %v", name, args)
		}
		return commandResult{stdout: []byte(`{"format":"PNG","width":10,"height":20,"mode":"RGB","frames":1}`)}
	}
	code, out, err := execute("see", []string{"--input", image, "--tool", "imgsee-test", "--format", "text", "--width", "80"}, "")
	spoken := strings.Join(strings.Fields(out), " ")
	if code != 0 || !strings.Contains(spoken, "not a description of what the picture shows") || !strings.Contains(spoken, "Width 10") || err != "" {
		t.Fatalf("%d %s %s", code, out, err)
	}

	executeReadOnly = func(string, ...string) commandResult {
		return commandResult{stdout: []byte("\x1b[31mhidden\x1b[0m")}
	}
	code, out, err = execute("see", []string{"--input", image, "--tool", "imgsee-test"}, "")
	if code != 30 || strings.Contains(out, "\x1b") || strings.Contains(out, "hidden") || err != "" {
		t.Fatalf("controls %d %s %s", code, out, err)
	}

	pdf := filepath.Join(dir, "ticket.pdf")
	if err := os.WriteFile(pdf, []byte("%PDF"), 0o600); err != nil {
		t.Fatal(err)
	}
	executeReadOnly = func(_ string, args ...string) commandResult {
		if args[1] == "info" {
			return commandResult{stdout: []byte("Title: Change window\nPages: 2\nEncrypted: no\n")}
		}
		lines := make([]string, 45)
		for i := range lines {
			lines[i] = fmt.Sprintf("line %d", i+1)
		}
		return commandResult{stdout: []byte(strings.Join(lines, "\n"))}
	}
	code, out, err = execute("see", []string{"--input", pdf, "--tool", "pdfsee-test", "--text", "--format", "text", "--width", "80"}, "")
	pdfSpoken := strings.Join(strings.Fields(out), " ")
	if code != 30 || !strings.Contains(pdfSpoken, "truncated to 40") || !strings.Contains(pdfSpoken, "Text drawn inside images was not read") || err != "" {
		t.Fatalf("pdf %d %s %s", code, out, err)
	}

	t.Setenv("PATH", t.TempDir())
	called := false
	executeReadOnly = func(string, ...string) commandResult {
		called = true
		return commandResult{}
	}
	code, out, err = execute("see", []string{"--input", image, "--format", "text"}, "")
	if code != 30 || called || !strings.Contains(out, "imgsee is not on PATH") || err != "" {
		t.Fatalf("missing %d called=%v %s %s", code, called, out, err)
	}
}
