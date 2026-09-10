package toolkit

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

type appConfig struct {
	Version         string                      `json:"version" yaml:"version"`
	CredentialsFile string                      `json:"credentials_file,omitempty" yaml:"credentials_file,omitempty"`
	Defaults        map[string]any              `json:"defaults,omitempty" yaml:"defaults,omitempty"`
	Commands        map[string]map[string]any   `json:"commands,omitempty" yaml:"commands,omitempty"`
	AI              aiRoutingConfig             `json:"ai" yaml:"ai"`
	Providers       map[string]aiProviderConfig `json:"providers,omitempty" yaml:"providers,omitempty"`
	Audit           auditConfig                 `json:"audit" yaml:"audit"`
}

type aiRoutingConfig struct {
	Primary  string `json:"primary" yaml:"primary"`
	Backup   string `json:"backup" yaml:"backup"`
	Tertiary string `json:"tertiary" yaml:"tertiary"`
}

type aiProviderConfig struct {
	Model     string `json:"model,omitempty" yaml:"model,omitempty"`
	BaseURL   string `json:"base_url,omitempty" yaml:"base_url,omitempty"`
	APIKeyEnv string `json:"api_key_env,omitempty" yaml:"api_key_env,omitempty"`
}

type credentialStore struct {
	SchemaVersion string            `json:"schema_version"`
	Providers     map[string]string `json:"providers"`
}

var configurableFlags = map[string]map[string]bool{
	"pilot":               flagSet("format", "width", "state"),
	"context-acquire":     flagSet("kind"),
	"permission-diff":     flagSet("format", "width", "environment", "kind"),
	"monitor":             flagSet("width", "state", "syntax", "max-queue", "batch", "interval", "duration"),
	"audit":               flagSet("format", "width", "limit"),
	"log-read":            flagSet("format", "width", "syntax", "context", "max-groups", "state"),
	"markdown-view":       flagSet("format", "width", "toc", "state"),
	"to-markdown":         flagSet("from", "table-mode", "delimiter", "header", "sheet"),
	"context-summary":     flagSet("format", "width", "environment", "max-age"),
	"resource-walk":       flagSet("format", "width", "environment", "direction", "depth", "max-age"),
	"incident":            flagSet("format", "width", "state", "limit"),
	"runbook":             flagSet("format", "width", "state", "max-age"),
	"fleet-check":         flagSet("format", "width", "environment", "max-age", "baseline-max-age"),
	"report-read":         flagSet("width", "layout"),
	"doctor":              flagSet("format", "width", "environment", "sample"),
	"collect":             flagSet("kind", "region", "profile", "max-pages"),
	"changes":             flagSet("format", "width", "environment", "kind", "max-age", "baseline-max-age"),
	"kube-explain":        flagSet("format", "width", "environment", "max-age"),
	"ansible-watch":       flagSet("format", "width", "environment", "max-age"),
	"state-walk":          flagSet("format", "width", "state", "limit"),
	"network-check":       flagSet("format", "width", "environment", "timeout", "min-valid-for", "expect-status"),
	"tasks":               flagSet("format", "width", "registry"),
	"config-walk":         flagSet("format", "width", "state"),
	"spacelift-watch":     flagSet("format", "width", "environment"),
	"spacelift-check":     flagSet("format", "width", "environment", "max-age"),
	"spacelift-runs":      flagSet("format", "width", "environment", "max-age"),
	"spacelift-diff":      flagSet("format", "width", "environment", "max-age"),
	"plan-explain":        flagSet("format", "width", "environment"),
	"plan-diff":           flagSet("format", "width", "environment"),
	"iac-config-check":    flagSet("format", "width", "environment"),
	"iac-validate":        flagSet("format", "width", "environment", "engine"),
	"iac-context":         flagSet("format", "width", "environment", "max-age"),
	"command-gen":         flagSet("format", "width", "to", "region", "profile"),
	"ai-assist":           flagSet("action", "target", "domain", "max-input-bytes", "timeout"),
	"decompose":           flagSet("format", "from", "to", "step", "width", "region", "profile"),
	"hcl2aws":             flagSet("format", "step", "width", "region", "profile"),
	"hcl2ali":             flagSet("format", "step", "width", "region", "profile"),
	"ansible2aws":         flagSet("format", "step", "width", "region", "profile"),
	"ansible2ali":         flagSet("format", "step", "width", "region", "profile"),
	"review":              flagSet("format", "width", "environment", "policy", "repo-policy", "base"),
	"git-review":          flagSet("format", "width", "environment", "policy", "repo-policy", "base"),
	"config-explain":      flagSet("format", "syntax", "width", "values"),
	"config-diff":         flagSet("format", "syntax", "width", "values"),
	"config-set":          flagSet("syntax"),
	"config-remove":       flagSet("syntax"),
	"error-explain":       flagSet("format", "tool", "evidence-lines"),
	"ops-policy-check":    flagSet("format", "width", "environment", "policy"),
	"repo-policy-check":   flagSet("format", "width", "environment", "policy", "repo-policy"),
	"context":             flagSet("format", "width"),
	"watch":               flagSet("format", "width", "environment"),
	"jsonprobe-check":     flagSet("format", "width", "environment", "policy"),
	"tofu-check":          flagSet("format", "width", "environment", "policy", "engine"),
	"ansible-check":       flagSet("format", "width", "environment", "policy"),
	"cloud-context-check": flagSet("format", "width", "environment", "policy"),
}

func flagSet(values ...string) map[string]bool {
	result := make(map[string]bool, len(values))
	for _, value := range values {
		result[value] = true
	}
	return result
}

func defaultAppConfig() appConfig {
	return appConfig{
		Version:         "1",
		Audit:           auditConfig{File: "audit.jsonl", Output: "redacted", MaxOutputBytes: 65536},
		CredentialsFile: "credentials.json",
		Defaults:        map[string]any{"format": "text", "environment": "unknown", "width": 100},
		Commands: map[string]map[string]any{
			"ai-assist":      {"action": "explain", "target": "config", "domain": "auto", "max-input-bytes": 262144, "timeout": 60},
			"decompose":      {"from": "auto", "to": "aws", "format": "text", "width": 100},
			"review":         {"base": "HEAD", "repo-policy": ".rcdo/policy.yaml"},
			"config-explain": {"syntax": "auto", "values": true},
			"config-diff":    {"syntax": "auto", "values": true},
		},
		AI: aiRoutingConfig{Primary: "openai", Backup: "anthropic", Tertiary: "openrouter"},
		Providers: map[string]aiProviderConfig{
			"openai":     {APIKeyEnv: "OPENAI_API_KEY"},
			"anthropic":  {APIKeyEnv: "ANTHROPIC_API_KEY"},
			"openrouter": {APIKeyEnv: "OPENROUTER_API_KEY", BaseURL: "https://openrouter.ai/api/v1"},
		},
	}
}

func defaultAppConfigPath() (string, error) {
	if path := strings.TrimSpace(os.Getenv("RCDO_CONFIG")); path != "" {
		return path, nil
	}
	if path := strings.TrimSpace(os.Getenv("GIT_TOOLS_CONFIG")); path != "" {
		return path, nil
	}
	directory, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("find user config directory: %w", err)
	}
	preferred := filepath.Join(directory, "rcdo", "config.yaml")
	legacy := filepath.Join(directory, "git-tools", "config.yaml")
	if _, err := os.Stat(preferred); os.IsNotExist(err) {
		if _, legacyErr := os.Stat(legacy); legacyErr == nil {
			return legacy, nil
		}
	}
	return preferred, nil
}

func extractRuntimeConfigFlag(args []string) ([]string, string, error) {
	result := make([]string, 0, len(args))
	path := ""
	for index := 0; index < len(args); index++ {
		arg := args[index]
		if arg == "--config-file" {
			if index+1 >= len(args) {
				return nil, "", fmt.Errorf("--config-file requires a path")
			}
			path = args[index+1]
			index++
			continue
		}
		if strings.HasPrefix(arg, "--config-file=") {
			path = strings.TrimPrefix(arg, "--config-file=")
			continue
		}
		result = append(result, arg)
	}
	return result, path, nil
}

func applyConfiguredDefaults(command string, args []string, explicitPath string) ([]string, error) {
	allowed := configurableFlags[command]
	if len(allowed) == 0 {
		return args, nil
	}
	path := explicitPath
	if path == "" {
		var err error
		path, err = defaultAppConfigPath()
		if err != nil {
			return nil, err
		}
	}
	config, found, err := loadAppConfig(path)
	if err != nil {
		return nil, err
	}
	if !found {
		if explicitPath != "" || strings.TrimSpace(os.Getenv("RCDO_CONFIG")) != "" || strings.TrimSpace(os.Getenv("GIT_TOOLS_CONFIG")) != "" {
			return nil, fmt.Errorf("configuration %q does not exist", path)
		}
		return args, nil
	}
	values := map[string]any{}
	for key, value := range config.Defaults {
		values[normalizeConfigFlag(key)] = value
	}
	if oneOf(command, "hcl2aws", "hcl2ali", "ansible2aws", "ansible2ali") {
		if commandValues := config.Commands["decompose"]; commandValues != nil {
			for key, value := range commandValues {
				values[normalizeConfigFlag(key)] = value
			}
		}
	}
	if commandValues := config.Commands[command]; commandValues != nil {
		for key, value := range commandValues {
			values[normalizeConfigFlag(key)] = value
		}
	}
	if command == "git-review" {
		if commandValues := config.Commands["review"]; commandValues != nil {
			for key, value := range commandValues {
				values[normalizeConfigFlag(key)] = value
			}
		}
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	prefix := []string{}
	for _, key := range keys {
		if !allowed[key] || hasCLIFlag(args, key) {
			continue
		}
		value, ok := configFlagValue(values[key])
		if !ok || value == "" {
			continue
		}
		prefix = append(prefix, "--"+key+"="+value)
	}
	// Subcommands must remain first: these readers consume the mode before flags.
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		out := append([]string{args[0]}, prefix...)
		return append(out, args[1:]...), nil
	}
	return append(prefix, args...), nil
}

func normalizeConfigFlag(value string) string {
	return strings.ReplaceAll(strings.TrimSpace(value), "_", "-")
}
func hasCLIFlag(args []string, name string) bool {
	for _, arg := range args {
		if arg == "--" {
			break
		}
		flagName := strings.TrimPrefix(strings.TrimPrefix(arg, "-"), "-")
		if strings.HasPrefix(arg, "-") && (flagName == name || strings.HasPrefix(flagName, name+"=")) {
			return true
		}
	}
	return false
}
func configFlagValue(value any) (string, bool) {
	switch typed := value.(type) {
	case string:
		return typed, true
	case bool:
		return strconv.FormatBool(typed), true
	case int:
		return strconv.Itoa(typed), true
	case int64:
		return strconv.FormatInt(typed, 10), true
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64), true
	default:
		return "", false
	}
}

func loadAppConfig(path string) (appConfig, bool, error) {
	var config appConfig
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return config, false, nil
	}
	if err != nil {
		return config, false, fmt.Errorf("read config %q: %w", path, err)
	}
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&config); err != nil {
		return config, false, fmt.Errorf("parse config %q: %w", path, err)
	}
	if err := validateAppConfig(config); err != nil {
		return config, false, fmt.Errorf("config %q: %w", path, err)
	}
	return config, true, nil
}

func validateAppConfig(config appConfig) error {
	if err := validateAuditConfig(config.Audit); err != nil {
		return err
	}
	if config.Version != "1" {
		return fmt.Errorf("version must be 1")
	}
	seen := map[string]bool{}
	for _, provider := range []string{config.AI.Primary, config.AI.Backup, config.AI.Tertiary} {
		if provider == "" {
			return fmt.Errorf("ai primary, backup, and tertiary providers are required")
		}
		if seen[provider] {
			return fmt.Errorf("AI provider %q occupies more than one routing slot", provider)
		}
		seen[provider] = true
		if _, ok := config.Providers[provider]; !ok {
			return fmt.Errorf("AI provider %q has no provider configuration", provider)
		}
	}
	allowedDefaults := flagSet("format", "environment", "width", "policy", "repo-policy", "base", "syntax", "values", "tool", "evidence-lines")
	for key := range config.Defaults {
		if !allowedDefaults[normalizeConfigFlag(key)] {
			return fmt.Errorf("unknown default %q", key)
		}
	}
	for command, values := range config.Commands {
		allowed := configurableFlags[command]
		if len(allowed) == 0 {
			return fmt.Errorf("unknown configurable command %q", command)
		}
		for key := range values {
			if !allowed[normalizeConfigFlag(key)] {
				return fmt.Errorf("command %s does not support configured option %q", command, key)
			}
		}
	}
	return nil
}

func runAppConfig(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" {
		printAppConfigHelp(stdout)
		return nil
	}
	mode, args := args[0], args[1:]
	var path string
	fs := flag.NewFlagSet("config "+mode, flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&path, "file", "", "configuration file; default is the user configuration path")
	var force, fromStdin bool
	var key, value, provider, fromEnv, primary, backup, tertiary string
	switch mode {
	case "init":
		fs.BoolVar(&force, "force", false, "replace an existing configuration file")
	case "set":
		fs.StringVar(&key, "key", "", "configuration path such as defaults.format")
		fs.StringVar(&value, "value", "", "value as JSON or literal text")
	case "credential-set":
		fs.StringVar(&provider, "provider", "", "provider name: openai, anthropic, or openrouter")
		fs.StringVar(&fromEnv, "from-env", "", "copy the key from this environment variable")
		fs.BoolVar(&fromStdin, "stdin", false, "read the key from standard input")
	case "credential-remove":
		fs.StringVar(&provider, "provider", "", "provider name: openai, anthropic, or openrouter")
	case "ai-order":
		fs.StringVar(&primary, "primary", "", "primary AI provider")
		fs.StringVar(&backup, "backup", "", "backup AI provider")
		fs.StringVar(&tertiary, "tertiary", "", "tertiary AI provider")
	case "show", "path", "ai-status", "ai-resolve":
	default:
		return fmt.Errorf("unknown config operation %q", mode)
	}
	setAccessibleUsage(fs, "config "+mode, stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected arguments: %v", fs.Args())
	}
	if path == "" {
		var err error
		path, err = defaultAppConfigPath()
		if err != nil {
			return err
		}
	}
	switch mode {
	case "path":
		config, found, err := loadAppConfig(path)
		if err != nil {
			return err
		}
		if !found {
			config = appConfig{CredentialsFile: "credentials.json"}
		}
		fmt.Fprintf(stdout, "Configuration: %s\nCredentials: %s\n", path, credentialsPath(path, config))
		auditPath := config.Audit.File
		if auditPath == "" {
			auditPath = "audit.jsonl"
		}
		if !filepath.IsAbs(auditPath) {
			auditPath = filepath.Join(filepath.Dir(path), auditPath)
		}
		fmt.Fprintf(stdout, "Audit: %s (enabled: %t)\n", auditPath, config.Audit.Enabled)
		return nil
	case "init":
		return initializeAppConfig(path, force, stdout)
	case "show":
		config, found, err := loadAppConfig(path)
		if err != nil {
			return err
		}
		if !found {
			return fmt.Errorf("configuration %q does not exist; run config init", path)
		}
		data, _ := yaml.Marshal(redactedConfig(config))
		_, err = stdout.Write(data)
		return err
	case "set":
		if key == "" {
			return fmt.Errorf("set requires --key")
		}
		return setAppConfigValue(path, key, value, stdout)
	case "credential-set":
		return setCredential(path, provider, fromEnv, fromStdin, stdin, stdout)
	case "credential-remove":
		return removeCredential(path, provider, stdout)
	case "ai-order":
		return setAIOrder(path, primary, backup, tertiary, stdout)
	case "ai-status":
		return showAIStatus(path, stdout)
	case "ai-resolve":
		return showAIResolution(path, stdout)
	}
	return nil
}

func printAppConfigHelp(w io.Writer) {
	fmt.Fprintln(w, "config: toolkit defaults and AI credential routing")
	fmt.Fprintln(w, "Usage: rcdo config COMMAND [options]")
	fmt.Fprintln(w, "Commands:")
	for _, name := range []string{"init", "show", "path", "set", "ai-order", "credential-set", "credential-remove", "ai-status", "ai-resolve"} {
		fmt.Fprintln(w, "  "+name)
	}
}

func initializeAppConfig(path string, force bool, stdout io.Writer) error {
	if !force {
		if _, err := os.Stat(path); err == nil {
			return fmt.Errorf("configuration %q already exists; use --force to replace it", path)
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := yaml.Marshal(defaultAppConfig())
	if err != nil {
		return err
	}
	if force {
		if _, err := os.Stat(path); err == nil {
			return atomicReplace(path, data, stdout)
		}
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	fmt.Fprintf(stdout, "CONFIGURATION CREATED: %s\n", path)
	return nil
}

func setAppConfigValue(path, key, raw string, stdout io.Writer) error {
	if isSensitivePath(normalizeConfigPath(key)) {
		return fmt.Errorf("sensitive values must use config credential-set, not config set")
	}
	config, found, err := loadAppConfig(path)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("configuration %q does not exist; run config init", path)
	}
	data, _ := yaml.Marshal(config)
	var object any
	if err := yaml.Unmarshal(data, &object); err != nil {
		return err
	}
	object = normalizeConfigMaps(object)
	parts, err := parseConfigPath(key)
	if err != nil {
		return err
	}
	if len(parts) == 0 {
		return fmt.Errorf("configuration root cannot be replaced")
	}
	var value any
	if json.Unmarshal([]byte(raw), &value) != nil {
		value = raw
	}
	object, err = setConfigPath(object, parts, value)
	if err != nil {
		return err
	}
	updated, err := yaml.Marshal(object)
	if err != nil {
		return err
	}
	var checked appConfig
	decoder := yaml.NewDecoder(bytes.NewReader(updated))
	decoder.KnownFields(true)
	if err := decoder.Decode(&checked); err != nil {
		return err
	}
	if err := validateAppConfig(checked); err != nil {
		return err
	}
	return atomicReplace(path, updated, stdout)
}

func credentialsPath(configPath string, config appConfig) string {
	path := config.CredentialsFile
	if path == "" {
		path = "credentials.json"
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(filepath.Dir(configPath), path)
	}
	return path
}
func validAIProvider(provider string) bool {
	return provider == "openai" || provider == "anthropic" || provider == "openrouter"
}

func readCredentialStore(path string) (credentialStore, error) {
	store := credentialStore{SchemaVersion: "1", Providers: map[string]string{}}
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return store, nil
	}
	if err != nil {
		return store, err
	}
	if info.Mode().Perm()&0o077 != 0 {
		return store, fmt.Errorf("credential store %q permissions are %04o; require 0600", path, info.Mode().Perm())
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return store, err
	}
	if err := json.Unmarshal(data, &store); err != nil {
		return store, fmt.Errorf("parse credential store: %w", err)
	}
	if store.SchemaVersion != "1" {
		return store, fmt.Errorf("credential store schema_version must be 1")
	}
	if store.Providers == nil {
		store.Providers = map[string]string{}
	}
	return store, nil
}
func writeCredentialStore(path string, store credentialStore) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(store, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return os.WriteFile(path, data, 0o600)
	}
	return atomicReplace(path, data, io.Discard)
}

func setCredential(configPath, provider, fromEnv string, fromStdin bool, stdin io.Reader, stdout io.Writer) error {
	if !validAIProvider(provider) {
		return fmt.Errorf("--provider must be openai, anthropic, or openrouter")
	}
	if (fromEnv != "") == fromStdin {
		return fmt.Errorf("choose exactly one of --from-env or --stdin")
	}
	key := ""
	if fromEnv != "" {
		key = os.Getenv(fromEnv)
		if key == "" {
			return fmt.Errorf("environment variable %s is empty", fromEnv)
		}
	} else {
		data, err := io.ReadAll(io.LimitReader(stdin, 64*1024))
		if err != nil {
			return err
		}
		key = strings.TrimSpace(string(data))
		if key == "" {
			return fmt.Errorf("standard input contained no API key")
		}
	}
	config, found, err := loadAppConfig(configPath)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("configuration %q does not exist; run config init", configPath)
	}
	path := credentialsPath(configPath, config)
	store, err := readCredentialStore(path)
	if err != nil {
		return err
	}
	store.Providers[provider] = key
	if err := writeCredentialStore(path, store); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "CREDENTIAL STORED\nProvider: %s\nLocation: %s\nValue: [REDACTED]\n", provider, path)
	return nil
}
func removeCredential(configPath, provider string, stdout io.Writer) error {
	if !validAIProvider(provider) {
		return fmt.Errorf("--provider must be openai, anthropic, or openrouter")
	}
	config, found, err := loadAppConfig(configPath)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("configuration %q does not exist", configPath)
	}
	path := credentialsPath(configPath, config)
	store, err := readCredentialStore(path)
	if err != nil {
		return err
	}
	if _, ok := store.Providers[provider]; !ok {
		return fmt.Errorf("no stored credential for %s", provider)
	}
	delete(store.Providers, provider)
	if err := writeCredentialStore(path, store); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "CREDENTIAL REMOVED: %s\n", provider)
	return nil
}

func setAIOrder(configPath, primary, backup, tertiary string, stdout io.Writer) error {
	if !validAIProvider(primary) || !validAIProvider(backup) || !validAIProvider(tertiary) {
		return fmt.Errorf("--primary, --backup, and --tertiary must each name openai, anthropic, or openrouter")
	}
	config, found, err := loadAppConfig(configPath)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("configuration %q does not exist; run config init", configPath)
	}
	config.AI = aiRoutingConfig{Primary: primary, Backup: backup, Tertiary: tertiary}
	if err := validateAppConfig(config); err != nil {
		return err
	}
	data, err := yaml.Marshal(config)
	if err != nil {
		return err
	}
	return atomicReplace(configPath, data, stdout)
}

func showAIStatus(configPath string, stdout io.Writer) error {
	config, found, err := loadAppConfig(configPath)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("configuration %q does not exist", configPath)
	}
	store, err := readCredentialStore(credentialsPath(configPath, config))
	if err != nil {
		return err
	}
	slots := []struct{ name, provider string }{{"primary", config.AI.Primary}, {"backup", config.AI.Backup}, {"tertiary", config.AI.Tertiary}}
	fmt.Fprintln(stdout, "AI PROVIDER STATUS")
	for _, slot := range slots {
		definition := config.Providers[slot.provider]
		source, ok := aiCredentialSource(slot.provider, definition, store)
		available := "no"
		if ok {
			available = "yes"
		}
		fmt.Fprintf(stdout, "Slot: %s\nProvider: %s\nAvailable: %s\nCredential source: %s\n", slot.name, slot.provider, available, source)
		if definition.Model != "" {
			fmt.Fprintf(stdout, "Model: %s\n", definition.Model)
		}
		fmt.Fprintln(stdout)
	}
	return nil
}

func showAIResolution(configPath string, stdout io.Writer) error {
	config, found, err := loadAppConfig(configPath)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("configuration %q does not exist", configPath)
	}
	store, err := readCredentialStore(credentialsPath(configPath, config))
	if err != nil {
		return err
	}
	slots := []struct{ name, provider string }{{"primary", config.AI.Primary}, {"backup", config.AI.Backup}, {"tertiary", config.AI.Tertiary}}
	for _, slot := range slots {
		definition := config.Providers[slot.provider]
		source, available := aiCredentialSource(slot.provider, definition, store)
		if !available {
			continue
		}
		fmt.Fprintf(stdout, "AI PROVIDER RESOLUTION\nSelected slot: %s\nProvider: %s\nCredential source: %s\n", slot.name, slot.provider, source)
		if definition.Model != "" {
			fmt.Fprintf(stdout, "Model: %s\n", definition.Model)
		}
		if definition.BaseURL != "" {
			fmt.Fprintf(stdout, "Base URL: %s\n", definition.BaseURL)
		}
		return nil
	}
	return fmt.Errorf("no configured AI provider has an available credential")
}

func aiCredentialSource(provider string, definition aiProviderConfig, store credentialStore) (string, bool) {
	if definition.APIKeyEnv != "" && os.Getenv(definition.APIKeyEnv) != "" {
		return "environment " + definition.APIKeyEnv, true
	}
	if store.Providers[provider] != "" {
		return "credential store", true
	}
	return "missing", false
}
func redactedConfig(config appConfig) appConfig { return config }
