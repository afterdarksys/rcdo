package toolkit

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
)

func mockAWSCollector(t *testing.T, fn func([]string) commandResult) {
	t.Helper()
	old := executeReadOnly
	t.Cleanup(func() { executeReadOnly = old })
	executeReadOnly = func(name string, args ...string) commandResult {
		if name != "aws" {
			t.Fatalf("unexpected CLI %s", name)
		}
		joined := strings.Join(args, " ")
		for _, want := range []string{"--region us-east-1", "--no-cli-pager", "--no-cli-auto-prompt"} {
			if !strings.Contains(joined, want) {
				t.Fatalf("missing %s in %s", want, joined)
			}
		}
		return fn(args)
	}
}

const testAWSIdentity = `{"Account":"111111111111","Arn":"arn:aws:iam::111111111111:user/test"}`
const testInstancePage = `{"Reservations":[{"Instances":[{"InstanceId":"i-1","InstanceType":"t3.micro","ImageId":"ami-1","State":{"Name":"running"},"VpcId":"vpc-1","SubnetId":"subnet-1","SecurityGroups":[{"GroupId":"sg-1"}]}]}]}`

func TestCollectIdentityGateAndNoOverwrite(t *testing.T) {
	calls := 0
	mockAWSCollector(t, func(args []string) commandResult {
		calls++
		if args[0] != "sts" {
			t.Fatal("inventory requested on account mismatch")
		}
		return commandResult{stdout: []byte(testAWSIdentity)}
	})
	code, out, e := execute("collect", []string{"--kind", "relations", "--region", "us-east-1", "--expect-account", "222222222222"}, "")
	if code != 30 || calls != 1 || !strings.Contains(out, `"complete": false`) || !strings.Contains(e, "account differs") {
		t.Fatalf("%d %s %s", code, out, e)
	}
	path := writeFixture(t, t.TempDir(), "context.json", "original")
	code, _, _ = execute("collect", []string{"--region", "us-east-1", "--expect-account", "111111111111", "--output", path}, "")
	if code != 2 {
		t.Fatal("overwrote existing output")
	}
	b, _ := os.ReadFile(path)
	if string(b) != "original" {
		t.Fatal("output changed")
	}
}
func TestCollectPaginationFailureAndCompatibleGraph(t *testing.T) {
	page := 0
	mockAWSCollector(t, func(args []string) commandResult {
		if args[0] == "sts" {
			return commandResult{stdout: []byte(testAWSIdentity)}
		}
		page++
		if page == 1 {
			return commandResult{stdout: []byte(strings.TrimSuffix(testInstancePage, "}") + `,"NextToken":"next"}`)}
		}
		if !strings.Contains(strings.Join(args, " "), "--starting-token next") {
			t.Fatal("continuation missing")
		}
		return commandResult{err: errors.New("denied"), stderr: "secret detail"}
	})
	code, out, e := execute("collect", []string{"--kind", "relations", "--region", "us-east-1", "--expect-account", "111111111111"}, "")
	if code != 30 || page != 2 || strings.Contains(e, "secret detail") {
		t.Fatalf("%d %s %s", code, out, e)
	}
	var g relationGraph
	if err := json.Unmarshal([]byte(out), &g); err != nil {
		t.Fatal(err)
	}
	if len(g.Nodes) != 4 || len(g.Edges) != 3 || *g.Complete {
		t.Fatalf("%+v", g)
	}
	code, _, e = execute("resource-walk", []string{"--resource", "i-1"}, out)
	if code != 30 {
		t.Fatalf("reader %d %s", code, e)
	}
}
func TestCollectFleetAndIdentityChange(t *testing.T) {
	identityCalls := 0
	changed := false
	mockAWSCollector(t, func(args []string) commandResult {
		if args[0] == "sts" {
			identityCalls++
			id := testAWSIdentity
			if changed && identityCalls%2 == 0 {
				id = strings.ReplaceAll(id, "111111111111", "222222222222")
			}
			return commandResult{stdout: []byte(id)}
		}
		return commandResult{stdout: []byte(testInstancePage)}
	})
	manifest := writeFixture(t, t.TempDir(), "manifest.json", `{"schema_version":"1","hosts":[{"id":"i-1","platform":"aws-ec2","baseline":"b"}],"baselines":{"b":{"platform":"aws-ec2","values":{"state":"running"},"source":"test","collected_at":"2026-09-10T00:00:00Z"}},"observations":[]}`)
	args := []string{"--kind", "fleet", "--region", "us-east-1", "--expect-account", "111111111111", "--manifest", manifest}
	code, out, e := execute("collect", args, "")
	if code != 0 {
		t.Fatalf("%d %s %s", code, out, e)
	}
	var b fleetBundle
	if strictJSON([]byte(out), &b) != nil || len(b.Observations) != 1 || b.Observations[0].Values["state"] != "running" {
		t.Fatal(out)
	}
	changed = true
	code, out, e = execute("collect", args, "")
	if code != 30 || !strings.Contains(e, "inventory discarded") {
		t.Fatalf("%d %s %s", code, out, e)
	}
	_ = json.Unmarshal([]byte(out), &b)
	if len(b.Observations) != 0 {
		t.Fatal("cross identity inventory retained")
	}
}
