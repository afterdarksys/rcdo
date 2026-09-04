package toolkit

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func writeAIConfig(t *testing.T, baseURL string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	config := `version: "1"
credentials_file: credentials.json
defaults: {}
commands: {}
ai:
  primary: openai
  backup: anthropic
  tertiary: openrouter
providers:
  openai:
    model: test-openai
    base_url: ` + baseURL + `/v1
    api_key_env: RCDO_TEST_OPENAI_KEY
  anthropic:
    api_key_env: RCDO_TEST_ANTHROPIC_KEY
  openrouter:
    api_key_env: RCDO_TEST_OPENROUTER_KEY
`
	if err := os.WriteFile(path, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestAIAssistRequiresExplicitActivation(t *testing.T) {
	configPath := writeAIConfig(t, "http://127.0.0.1:1")
	code, _, stderr := execute("ai-assist", []string{"--config-file", configPath}, "resource {}")
	if code != 2 || !strings.Contains(stderr, "pass --ai") {
		t.Fatalf("code=%d stderr=%q", code, stderr)
	}
}

func TestAIAssistCallsConfiguredOpenAIResponsesAPIAndRedacts(t *testing.T) {
	t.Setenv("RCDO_TEST_OPENAI_KEY", "test-key-not-for-output")
	t.Setenv("RCDO_TEST_ANTHROPIC_KEY", "")
	t.Setenv("RCDO_TEST_OPENROUTER_KEY", "")
	var received map[string]any
	originalClient := aiHTTPClient
	defer func() { aiHTTPClient = originalClient }()
	aiHTTPClient = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Path != "/v1/responses" {
			t.Errorf("path=%q", request.URL.Path)
		}
		if request.Header.Get("Authorization") != "Bearer test-key-not-for-output" {
			t.Errorf("authorization header missing")
		}
		if err := json.NewDecoder(request.Body).Decode(&received); err != nil {
			t.Error(err)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"output":[{"type":"message","content":[{"type":"output_text","text":"Use a narrower IAM policy."}]}]}`)),
			Request:    request,
		}, nil
	})}

	configPath := writeAIConfig(t, "https://provider.test")
	input := "provider: aws\napi_key: must-not-leave-process\njson: {\"client_secret\":\"also-must-not-leave\"}\nresource: public_bucket\n"
	code, stdout, stderr := execute("ai-assist", []string{"--config-file", configPath, "--ai", "--action", "debug", "--domain", "aws"}, input)
	if code != 0 || stderr != "" || !strings.Contains(stdout, "Use a narrower IAM policy") || !strings.Contains(stdout, "Provider: openai") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	prompt, _ := received["input"].(string)
	if strings.Contains(prompt, "must-not-leave") || !strings.Contains(prompt, "[REDACTED]") {
		t.Fatalf("input was not redacted: %q", prompt)
	}
	if received["store"] != false {
		t.Fatalf("OpenAI request must disable storage: %#v", received)
	}
}

func TestRepositoryAIContextIsBoundedAndSkipsSensitiveFiles(t *testing.T) {
	repository := t.TempDir()
	if err := os.WriteFile(filepath.Join(repository, "main.tf"), []byte("resource \"aws_s3_bucket\" \"example\" {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repository, "credentials.json"), []byte(`{"token":"do-not-send"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(repository, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repository, ".git", "config"), []byte("private"), 0o600); err != nil {
		t.Fatal(err)
	}

	context, truncated, err := collectRepositoryAIContext(repository, 4096)
	if err != nil || truncated {
		t.Fatalf("truncated=%v err=%v", truncated, err)
	}
	if !strings.Contains(context, "main.tf") || strings.Contains(context, "do-not-send") || strings.Contains(context, ".git") {
		t.Fatalf("unexpected repository context: %q", context)
	}
}

func TestAnthropicAndOpenRouterAdapters(t *testing.T) {
	originalClient := aiHTTPClient
	defer func() { aiHTTPClient = originalClient }()
	tests := []struct {
		provider string
		path     string
		body     string
		want     string
	}{
		{"anthropic", "/v1/messages", `{"content":[{"type":"text","text":"anthropic answer"}]}`, "anthropic answer"},
		{"openrouter", "/v1/chat/completions", `{"choices":[{"message":{"content":"openrouter answer"}}]}`, "openrouter answer"},
	}
	for _, test := range tests {
		t.Run(test.provider, func(t *testing.T) {
			aiHTTPClient = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
				if request.URL.Path != test.path {
					t.Errorf("path=%q want=%q", request.URL.Path, test.path)
				}
				return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(test.body)), Header: http.Header{}, Request: request}, nil
			})}
			selection := aiProviderSelection{Name: test.provider, APIKey: "secret", Definition: aiProviderConfig{Model: "test-model", BaseURL: "https://provider.test/v1"}}
			answer, err := callAIProvider(t.Context(), selection, "inspect this")
			if err != nil || answer != test.want {
				t.Fatalf("answer=%q err=%v", answer, err)
			}
		})
	}
}

func TestAIActivationCannotBeEnabledByConfiguration(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	if code, _, stderr := execute("config", []string{"init", "--file", configPath}, ""); code != 0 {
		t.Fatalf("init code=%d stderr=%q", code, stderr)
	}
	code, _, stderr := execute("config", []string{"set", "--file", configPath, "--key", "commands.ai-assist.ai", "--value", "true"}, "")
	if code != 2 || !strings.Contains(stderr, "does not support configured option") {
		t.Fatalf("code=%d stderr=%q", code, stderr)
	}
}

func TestAIOutputRemovesTerminalControls(t *testing.T) {
	got := sanitizeAIOutput("\x1b[31mDanger\x1b[0m\tcheck\r\nnext")
	if strings.ContainsAny(got, "\x1b\r\t") || !strings.Contains(got, "Danger") || !strings.Contains(got, "next") {
		t.Fatalf("unsafe output: %q", got)
	}
}
