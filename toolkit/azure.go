package toolkit

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// Threats: acting in the wrong Azure subscription is the failure this check
// exists to catch. It reads account metadata twice and compares the explicit
// subscription and tenant. It does not request an access token, and it does
// not prove resource permissions or that a later deployment succeeded.
// Azure CLI diagnostics are discarded because error text can echo credentials.
// A mismatch, a missing principal, or a changed second read fails closed.

var azureGUID = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func acquireAzure(c *contextAcquirer, subscription, tenant string) (map[string]string, error) {
	if !azureGUID.MatchString(subscription) || !azureGUID.MatchString(tenant) {
		return nil, fmt.Errorf("Azure requires explicit subscription and tenant GUIDs")
	}
	read := func() (map[string]string, error) {
		raw, err := c.call("az", "account", "show", "--subscription", subscription, "--output", "json", "--only-show-errors", "--query", "{id:id,tenantId:tenantId,name:name,state:state,principal:user.name}")
		if err != nil {
			return nil, err
		}
		var shown struct {
			ID        string `json:"id"`
			TenantID  string `json:"tenantId"`
			Name      string `json:"name"`
			State     string `json:"state"`
			Principal string `json:"principal"`
		}
		if json.Unmarshal(raw, &shown) != nil {
			return nil, fmt.Errorf("Azure account metadata was incomplete")
		}
		shown.ID = strings.ToLower(shown.ID)
		shown.TenantID = strings.ToLower(shown.TenantID)
		if !azureGUID.MatchString(shown.ID) || !azureGUID.MatchString(shown.TenantID) {
			return nil, fmt.Errorf("Azure account metadata was incomplete")
		}
		if shown.ID != strings.ToLower(subscription) || shown.TenantID != strings.ToLower(tenant) {
			return nil, fmt.Errorf("Azure subscription %s tenant %s does not match expected subscription %s tenant %s", shown.ID, shown.TenantID, strings.ToLower(subscription), strings.ToLower(tenant))
		}
		if shown.State != "Enabled" || !operationLabel(shown.Principal) || !operationLabel(shown.Name) || auditText(shown.Name) != shown.Name || auditText(shown.Principal) != shown.Principal {
			return nil, fmt.Errorf("Azure subscription is not enabled or its identity labels are unsafe")
		}
		return map[string]string{"subscription": shown.ID, "tenant": shown.TenantID, "principal": shown.Principal, "state": shown.State, "name": shown.Name}, nil
	}
	first, err := read()
	if err != nil {
		return nil, err
	}
	second, err := read()
	if err != nil {
		return nil, err
	}
	if len(first) != len(second) {
		return nil, fmt.Errorf("Azure identity changed during acquisition")
	}
	for key, value := range first {
		if second[key] != value {
			return nil, fmt.Errorf("Azure identity changed during acquisition")
		}
	}
	return first, nil
}
