package toolkit

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestHCL2AWSBuildsDependencyOrderedManualPlan(t *testing.T) {
	input := `resource "aws_subnet" "web" {
  vpc_id    = aws_vpc.main.id
  cidr_block = "10.0.1.0/24"
}
resource "aws_vpc" "main" {
  cidr_block = "10.0.0.0/16"
}`
	code, stdout, stderr := execute("hcl2aws", nil, input)
	if code != 0 || stderr != "" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if strings.Index(stdout, "ID: aws_vpc.main") > strings.Index(stdout, "ID: aws_subnet.web") {
		t.Fatalf("dependency was not ordered before dependent:\n%s", stdout)
	}
	for _, expected := range []string{
		"Execution: NOT RUN",
		"aws ec2 create-vpc",
		"aws ec2 create-subnet",
		`--vpc-id "$RCDO_AWS_VPC_MAIN_ID"`,
		"Capture Vpc.VpcId",
		"aws ec2 delete-subnet",
	} {
		if !strings.Contains(stdout, expected) {
			t.Fatalf("missing %q:\n%s", expected, stdout)
		}
	}
}

func TestDecomposeStepSelectsOneOperation(t *testing.T) {
	input := `resource "aws_vpc" "main" {
  cidr_block = "10.0.0.0/16"
}
resource "aws_subnet" "web" {
  vpc_id = aws_vpc.main.id
  cidr_block = "10.0.1.0/24"
}`
	code, stdout, stderr := execute("decompose", []string{"--from", "hcl", "--to", "aws", "--step", "2"}, input)
	if code != 0 || stderr != "" || strings.Contains(stdout, "STEP 1") || !strings.Contains(stdout, "STEP 2") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestHCLDecompositionFailsClosedOnRuntimeExpressions(t *testing.T) {
	input := `resource "aws_vpc" "main" { cidr_block = var.vpc_cidr }`
	code, stdout, stderr := execute("hcl2aws", []string{"--format", "json"}, input)
	if code != 30 || stderr != "" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	var plan decompositionPlan
	if err := json.Unmarshal([]byte(stdout), &plan); err != nil {
		t.Fatal(err)
	}
	if plan.Status != "incomplete" || len(plan.Unresolved) == 0 || !strings.Contains(plan.Steps[0].Command, "<expression:") {
		t.Fatalf("plan=%#v", plan)
	}
}

func TestAnsible2AWSBuildsCLIExamples(t *testing.T) {
	input := `- name: network
  hosts: localhost
  tasks:
    - name: Create VPC
      amazon.aws.ec2_vpc_net:
        name: beta-vpc
        cidr_block: 10.20.0.0/16
    - name: Create instance
      amazon.aws.ec2_instance:
        image_id: ami-example
        instance_type: t3.micro
        state: present
`
	code, stdout, stderr := execute("ansible2aws", nil, input)
	if code != 0 || stderr != "" || !strings.Contains(stdout, "aws ec2 create-vpc") || !strings.Contains(stdout, "--tag-specifications") || !strings.Contains(stdout, "aws ec2 run-instances") || !strings.Contains(stdout, "Depends on: ansible.play1.task1") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestHCL2AliBuildsCLIExamples(t *testing.T) {
	input := `resource "alicloud_vpc" "main" {
  vpc_name  = "beta-vpc"
  cidr_block = "10.30.0.0/16"
}`
	code, stdout, stderr := execute("hcl2ali", []string{"--region", "cn-hangzhou", "--profile", "work"}, input)
	if code != 0 || stderr != "" || !strings.Contains(stdout, "aliyun vpc CreateVpc") || !strings.Contains(stdout, "--VpcName 'beta-vpc'") || !strings.Contains(stdout, "--RegionId 'cn-hangzhou'") || !strings.Contains(stdout, "--profile 'work'") || !strings.Contains(stdout, "DeleteVpc") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestAliDecompositionRequiresRegion(t *testing.T) {
	input := `resource "alicloud_vpc" "main" { cidr_block = "10.31.0.0/16" }`
	code, stdout, stderr := execute("hcl2ali", nil, input)
	if code != 30 || stderr != "" || !strings.Contains(stdout, "<required:region>") || !strings.Contains(stdout, "explicit AliCloud region") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestDecomposeRedactsSecretsAndQuotesShellValues(t *testing.T) {
	input := `resource "aws_iam_role" "main" {
  name = "role'; echo unsafe"
  assume_role_policy = "{}"
  client_secret = "secret-value"
}`
	code, stdout, stderr := execute("hcl2aws", nil, input)
	if code != 30 || stderr != "" || strings.Contains(stdout, "secret-value") || !strings.Contains(stdout, "was redacted") || !strings.Contains(stdout, `'"'"'`) {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestUnsupportedResourceProducesIncompletePlan(t *testing.T) {
	input := `resource "aws_lambda_function" "worker" { function_name = "worker" }`
	code, stdout, stderr := execute("hcl2aws", nil, input)
	if code != 30 || stderr != "" || !strings.Contains(stdout, "Status: INCOMPLETE") || !strings.Contains(stdout, "unsupported") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestHCLDependsOnIsOrderedWithoutPretendingARNIsAnID(t *testing.T) {
	input := `resource "aws_vpc" "main" {
  cidr_block = "10.0.0.0/16"
}
resource "aws_iam_role" "main" {
  name = "demo-role"
  assume_role_policy = aws_vpc.main.arn
  depends_on = [aws_vpc.main]
}`
	code, stdout, stderr := execute("hcl2aws", nil, input)
	if code != 30 || stderr != "" || !strings.Contains(stdout, "Depends on: aws_vpc.main") || !strings.Contains(stdout, "expression requiring evaluated state") || strings.Contains(stdout, `--assume-role-policy-document "$RCDO_AWS_VPC_MAIN_ID"`) {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestAnsibleDeletionNeverBecomesCreateCommand(t *testing.T) {
	input := `- hosts: localhost
  tasks:
    - name: Remove VPC
      amazon.aws.ec2_vpc_net:
        name: old-vpc
        cidr_block: 10.50.0.0/16
        state: absent
`
	code, stdout, stderr := execute("ansible2aws", nil, input)
	if code != 30 || stderr != "" || strings.Contains(stdout, "aws ec2 create-vpc") || !strings.Contains(stdout, "deletion task was not converted") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestS3CreationRequiresRegionReview(t *testing.T) {
	input := `resource "aws_s3_bucket" "main" { bucket = "globally-unique-example" }`
	code, stdout, stderr := execute("hcl2aws", nil, input)
	if code != 30 || stderr != "" || !strings.Contains(stdout, "aws s3api create-bucket") || !strings.Contains(stdout, "region-specific") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestS3CreationAddsRegionalLocationConstraint(t *testing.T) {
	input := `resource "aws_s3_bucket" "main" { bucket = "globally-unique-example" }`
	code, stdout, stderr := execute("hcl2aws", []string{"--region", "us-west-2"}, input)
	if code != 0 || stderr != "" || !strings.Contains(stdout, "LocationConstraint=us-west-2") || !strings.Contains(stdout, "--region 'us-west-2'") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestAliOSSDoesNotEmitDeprecatedCommand(t *testing.T) {
	input := `resource "alicloud_oss_bucket" "main" { bucket = "example" }`
	code, stdout, stderr := execute("hcl2ali", []string{"--region", "cn-hangzhou"}, input)
	if code != 30 || stderr != "" || strings.Contains(stdout, "PutBucket") || !strings.Contains(stdout, "unsupported") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestDecomposeRejectsUnsafeCLIContext(t *testing.T) {
	input := `resource "aws_vpc" "main" { cidr_block = "10.0.0.0/16" }`
	code, _, stderr := execute("hcl2aws", []string{"--profile", "work;echo"}, input)
	if code != 2 || !strings.Contains(stderr, "unsupported characters") {
		t.Fatalf("code=%d stderr=%q", code, stderr)
	}
}

func TestDecomposeAliasesInheritConfiguredDefaults(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	if code, _, stderr := execute("config", []string{"init", "--file", configPath}, ""); code != 0 {
		t.Fatalf("init code=%d stderr=%q", code, stderr)
	}
	if code, _, stderr := execute("config", []string{"set", "--file", configPath, "--key", "commands.decompose.format", "--value", "json"}, ""); code != 0 {
		t.Fatalf("set code=%d stderr=%q", code, stderr)
	}
	input := `resource "aws_vpc" "main" { cidr_block = "10.0.0.0/16" }`
	code, stdout, stderr := execute("hcl2aws", []string{"--config-file", configPath}, input)
	if code != 0 || stderr != "" || !strings.HasPrefix(stdout, "{\n") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}
