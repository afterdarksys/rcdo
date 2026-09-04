package toolkit

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"git-tools/finding"
)

type policyFile struct {
	Suppressions []struct {
		IDPrefix string `json:"id_prefix"`
		Resource string `json:"resource"`
		Owner    string `json:"owner"`
		Reason   string `json:"reason"`
		Expires  string `json:"expires"`
	} `json:"suppressions"`
}

func applyPolicy(path string, report finding.Report, now time.Time) (finding.Report, error) {
	if path == "" {
		return report, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return report, fmt.Errorf("read policy %q: %w", path, err)
	}
	var policy policyFile
	if err := json.Unmarshal(data, &policy); err != nil {
		return report, fmt.Errorf("parse policy %q: %w", path, err)
	}
	remaining := make([]finding.Finding, 0, len(report.Findings))
	for _, item := range report.Findings {
		suppressed := false
		for index, exception := range policy.Suppressions {
			if exception.IDPrefix == "" || exception.Owner == "" || exception.Reason == "" || exception.Expires == "" {
				return report, fmt.Errorf("policy suppression %d requires id_prefix, owner, reason, and expires", index+1)
			}
			expires, parseErr := time.Parse("2006-01-02", exception.Expires)
			if parseErr != nil {
				return report, fmt.Errorf("policy suppression %d has invalid expires date: %w", index+1, parseErr)
			}
			if !expires.After(now) || !strings.HasPrefix(item.ID, exception.IDPrefix) {
				continue
			}
			if exception.Resource != "" {
				matched, matchErr := filepath.Match(exception.Resource, item.Resource)
				if matchErr != nil {
					return report, fmt.Errorf("policy suppression %d has invalid resource pattern: %w", index+1, matchErr)
				}
				if !matched {
					continue
				}
			}
			report.CompletedChecks = append(report.CompletedChecks,
				fmt.Sprintf("suppressed %s by %s until %s: %s", item.ID, exception.Owner, exception.Expires, exception.Reason))
			suppressed = true
			break
		}
		if !suppressed {
			remaining = append(remaining, item)
		}
	}
	report.Findings = remaining
	return report, nil
}
