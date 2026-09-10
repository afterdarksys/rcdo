package toolkit

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestCommandQuoteHelper(t *testing.T) {
	if os.Getenv("RCDO_QUOTE_HELPER") != "1" {
		return
	}
	for i, arg := range os.Args {
		if arg == "--" {
			_ = json.NewEncoder(os.Stdout).Encode(os.Args[i+1:])
			os.Exit(0)
		}
	}
	os.Exit(2)
}
func TestCommandQuotingRoundTripsInRealShells(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(t.TempDir(), "should-not-exist")
	values := []string{"", "two words", "O'Brien", `a"b`, "$(touch " + marker + ")", "`echo nope`", "x;y|z&", `C:\path\with space\`, "$RCDO_ID"}
	argv := append([]string{executable, "-test.run=TestCommandQuoteHelper", "--"}, values...)
	for _, shell := range []string{"posix", "powershell"} {
		t.Run(shell, func(t *testing.T) {
			quoted, err := quoteCommand(argv, shell)
			if err != nil {
				t.Fatal(err)
			}
			var cmd *exec.Cmd
			if shell == "posix" {
				cmd = exec.Command("/bin/sh", "-c", quoted)
			} else {
				path, e := exec.LookPath("pwsh")
				if e != nil {
					t.Skip("PowerShell unavailable; POSIX round-trip still tested")
				}
				cmd = exec.Command(path, "-NoLogo", "-NoProfile", "-NonInteractive", "-Command", "$PSNativeCommandArgumentPassing = 'Standard'; "+quoted)
			}
			cmd.Env = append(os.Environ(), "RCDO_QUOTE_HELPER=1")
			out, e := cmd.CombinedOutput()
			if e != nil {
				t.Fatalf("%v %s", e, out)
			}
			var got []string
			if e = json.Unmarshal(out, &got); e != nil || !reflect.DeepEqual(got, values) {
				t.Fatalf("round trip %v %q expected %q", e, got, values)
			}
		})
	}
	if _, err = os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("command substitution executed")
	}
}
func TestCommandExplainCloudAndUnresolvedInputs(t *testing.T) {
	for _, shell := range []string{"posix", "powershell"} {
		code, out, e := execute("command-gen", []string{"--to", "aws", "--from", "hcl", "--region", "us-east-1", "--explain", "--shell", shell, "--format", "json"}, `resource "aws_vpc" "main" { cidr_block = "10.0.0.0/16" }`)
		if code != 0 || !strings.Contains(out, "remote-write") || !strings.Contains(out, "--cidr-block") || !strings.Contains(out, "network address range") {
			t.Fatalf("%d %s %s", code, out, e)
		}
	}
	code, out, e := execute("command-gen", []string{"--to", "aws", "--from", "hcl", "--region", "us-east-1", "--explain", "--format", "json"}, `resource "aws_subnet" "main" { cidr_block = "10.0.1.0/24" }`)
	if code != 30 || !strings.Contains(out, "requires vpc_id") || !strings.Contains(out, `"command":""`) {
		t.Fatalf("%d %s %s", code, out, e)
	}
	code, out, e = execute("command-gen", []string{"--to", "alicloud", "--from", "hcl", "--region", "cn-hangzhou", "--explain", "--shell", "powershell"}, `resource "alicloud_vpc" "main" {
 vpc_name = "example"
 cidr_block = "10.0.0.0/16"
}`)
	if code != 0 || !strings.Contains(out, "--RegionId") {
		t.Fatalf("%d %s %s", code, out, e)
	}
}
func TestCommandExplainIaCEffectsAndControls(t *testing.T) {
	for _, action := range []string{"validate", "plan", "fmt-check"} {
		code, out, e := execute("command-gen", []string{"--to", "tofu", "--action", action, "--directory", t.TempDir(), "--explain", "--format", "json"}, `resource "null_resource" "example" {}`)
		if code != 0 {
			t.Fatalf("%d %s %s", code, out, e)
		}
		var r commandRecipe
		if json.Unmarshal([]byte(out), &r) != nil || r.Executed || len(r.Parameters) == 0 {
			t.Fatal(out)
		}
		if (action == "plan") != (r.Effect == "local-plan-write-and-backend-lock") {
			t.Fatal(out)
		}
	}
	for _, s := range []string{"bad\x1b[2J", "bad\u202e", "bad\nline"} {
		if _, e := quoteCommand([]string{"tool", s}, "posix"); e == nil {
			t.Fatal("unsafe control accepted")
		}
	}
	if _, e := quoteCommand([]string{"tool", "curly’quote"}, "powershell"); e == nil {
		t.Fatal("ambiguous PowerShell smart quote accepted")
	}
}
