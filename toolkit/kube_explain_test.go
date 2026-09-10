package toolkit

import (
	"encoding/json"
	"errors"
	"git-tools/finding"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const kubeBadPod = `{"metadata":{"name":"api","namespace":"test","uid":"p1"},"spec":{"containers":[{"name":"app","env":[{"name":"PASSWORD","value":"private-value"}]}]},"status":{"phase":"Running","conditions":[{"type":"Ready","status":"False"}],"containerStatuses":[{"name":"app","ready":false,"restartCount":4,"state":{"waiting":{"reason":"CrashLoopBackOff"}}}]}}`

func TestKubeNativeScopePrivacyAndPartialCoverage(t *testing.T) {
	old := executeReadOnly
	defer func() { executeReadOnly = old }()
	calls := 0
	executeReadOnly = func(name string, args ...string) commandResult {
		calls++
		joined := strings.Join(args, " ")
		if name != "kubectl" || !strings.Contains(joined, "--context staging --namespace test") || !strings.Contains(joined, "--request-timeout=30s get") {
			t.Fatalf("unexpected command %s %s", name, joined)
		}
		if strings.Contains(joined, "get pods") {
			return commandResult{stdout: []byte(`{"kind":"PodList","apiVersion":"v1","items":[` + kubeBadPod + `]}`)}
		}
		if strings.Contains(joined, "get events") {
			return commandResult{err: errors.New("denied"), stderr: "private-error"}
		}
		return commandResult{stdout: []byte(`{"kind":"DeploymentList","apiVersion":"apps/v1","items":[]}`)}
	}
	path := filepath.Join(t.TempDir(), "snapshot.json")
	code, out, e := execute("kube-explain", []string{"--native", "--context", "staging", "--namespace", "test", "--save-snapshot", path}, "")
	if code != 30 || calls != 3 || !strings.Contains(out, "CrashLoopBackOff") || !strings.Contains(out, "events collection") || strings.Contains(out+e, "private-") {
		t.Fatalf("%d %s %s", code, out, e)
	}
	raw, err := os.ReadFile(path)
	if err != nil || strings.Contains(string(raw), "private-value") || strings.Contains(string(raw), "PASSWORD") {
		t.Fatalf("privacy: %s %v", raw, err)
	}
	code, _, _ = execute("kube-explain", []string{"--context", "staging", "--namespace", "test", "--input", path}, "")
	if code != 30 {
		t.Fatal("saved snapshot cannot be replayed", code)
	}
}
func TestKubeConditionsEventsAndScope(t *testing.T) {
	s := kubeSnapshot{SchemaVersion: "1", Context: "staging", Namespace: "test", Source: "fixture", CollectedAt: time.Now().UTC().Format(time.RFC3339Nano), Coverage: map[string]string{"pods": "pass", "deployments": "pass", "events": "pass"}, Pods: []kubePod{}, Deployments: []kubeDeployment{}, Events: []kubeEvent{}}
	var p kubePod
	_ = json.Unmarshal([]byte(kubeBadPod), &p)
	s.Pods = append(s.Pods, p)
	var d kubeDeployment
	_ = json.Unmarshal([]byte(`{"metadata":{"name":"api","namespace":"test","uid":"d1","generation":2},"spec":{"replicas":2},"status":{"observedGeneration":2,"replicas":2,"updatedReplicas":1,"availableReplicas":1,"conditions":[{"type":"Progressing","status":"False","reason":"ProgressDeadlineExceeded"}]}}`), &d)
	s.Deployments = append(s.Deployments, d)
	var event kubeEvent
	_ = json.Unmarshal([]byte(`{"metadata":{"name":"probe","namespace":"test","uid":"e1"},"involvedObject":{"name":"api","namespace":"test","kind":"Pod","uid":"p1"},"type":"Warning","reason":"Unhealthy","count":2}`), &event)
	s.Events = append(s.Events, event)
	r, e := explainKube(s, "staging", "test", time.Minute)
	if e != nil || r.Status() != finding.StatusBlocked {
		t.Fatal(r, e)
	}
	matched, deadline := false, false
	for _, f := range r.Findings {
		if strings.Contains(f.Title, "Warning event") && f.Resource == "pod/api" {
			matched = true
		}
		if strings.Contains(f.Title, "progress deadline") {
			deadline = true
		}
	}
	if !matched || !deadline {
		t.Fatal(r)
	}
	r, e = explainKube(s, "production", "test", time.Minute)
	if e != nil || len(r.Findings) != 1 || r.Findings[0].Severity != finding.SeverityCritical {
		t.Fatal(r, e)
	}
	s.CollectedAt = "2000-01-01T00:00:00Z"
	r, e = explainKube(s, "staging", "test", time.Minute)
	if e != nil || r.Status() != finding.StatusIncomplete {
		t.Fatal(r, e)
	}
}
func TestKubeRunningDoesNotMeanReady(t *testing.T) {
	raw := `{"schema_version":"1","context":"staging","namespace":"test","source":"fixture","collected_at":"` + time.Now().UTC().Format(time.RFC3339Nano) + `","coverage":{"pods":"pass","deployments":"pass","events":"pass"},"pods":[{"metadata":{"name":"api","namespace":"test","uid":"p1"},"spec":{"containers":[{"name":"app"}]},"status":{"phase":"Running","conditions":[{"type":"Ready","status":"True"}],"containerStatuses":[{"name":"app","ready":false,"restartCount":0,"state":{"running":{}}}]}}],"deployments":[],"events":[]}`
	code, out, e := execute("kube-explain", []string{"--context", "staging", "--namespace", "test"}, raw)
	if code != 10 || !strings.Contains(out, "Container is not ready") {
		t.Fatalf("%d %s %s", code, out, e)
	}
}

func TestKubeRejectsTerminalFormatScope(t *testing.T) {
	code, _, _ := execute("kube-explain", []string{"--native", "--context", "stage\u202e", "--namespace", "test"}, "")
	if code != 2 {
		t.Fatal("format-control scope accepted", code)
	}
}
