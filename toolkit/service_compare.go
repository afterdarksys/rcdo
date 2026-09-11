package toolkit

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math/big"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"git-tools/finding"
)

type serviceDeploymentBundle struct {
	SchemaVersion string              `json:"schema_version"`
	Deployments   []serviceDeployment `json:"deployments"`
}
type serviceDeployment struct {
	Name        string            `json:"name"`
	Cloud       string            `json:"cloud"`
	Account     string            `json:"account"`
	Location    string            `json:"location"`
	Source      string            `json:"source"`
	CollectedAt string            `json:"collected_at"`
	Complete    *bool             `json:"complete"`
	Resources   []serviceResource `json:"resources"`
}
type serviceResource struct {
	Key        string         `json:"key"`
	Service    string         `json:"service"`
	ID         string         `json:"id"`
	Properties map[string]any `json:"properties"`
}

// Bound exact decimal arithmetic avoids float rounding and unbounded exponents.
func serviceNumber(v any) (*big.Rat, bool) {
	n, ok := v.(json.Number)
	if !ok || len(n.String()) > 128 {
		return nil, false
	}
	s := strings.ToLower(n.String())
	if _, exp, has := strings.Cut(s, "e"); has {
		e, err := strconv.Atoi(exp)
		if err != nil || e < -308 || e > 308 {
			return nil, false
		}
	}
	return new(big.Rat).SetString(s)
}
func serviceValueValid(v any, f serviceField) bool {
	switch f.Type {
	case "boolean":
		_, ok := v.(bool)
		return ok
	case "string":
		s, ok := v.(string)
		return ok && operationLabel(s)
	case "number":
		_, ok := serviceNumber(v)
		return ok
	}
	return false
}
func equalServiceValue(a, b any, f serviceField) bool {
	if f.Type == "number" {
		x, _ := serviceNumber(a)
		y, _ := serviceNumber(b)
		return x.Cmp(y) == 0
	}
	return a == b // validation above limits values to bool/string
}

func selectServiceDeployments(b serviceDeploymentBundle, selection string) ([]serviceDeployment, error) {
	if b.SchemaVersion != "1" || len(b.Deployments) < 2 || len(b.Deployments) > 16 {
		return nil, fmt.Errorf("comparison requires version 1 and 2..16 deployments")
	}
	byName := map[string]serviceDeployment{}
	for _, d := range b.Deployments {
		if !serviceKeyPattern.MatchString(d.Name) || !serviceKeyPattern.MatchString(d.Cloud) || !operationLabel(d.Account) || !operationLabel(d.Location) || d.Resources == nil || len(d.Resources) > 256 {
			return nil, fmt.Errorf("each deployment requires name, cloud, account, location and 0..256 resources")
		}
		if _, ok := byName[d.Name]; ok {
			return nil, fmt.Errorf("duplicate deployment name")
		}
		keys, ids := map[string]bool{}, map[string]bool{}
		for _, r := range d.Resources {
			if !serviceKeyPattern.MatchString(r.Key) || !serviceKeyPattern.MatchString(r.Service) || !operationLabel(r.ID) || keys[r.Key] || ids[r.Service+"/"+r.ID] || len(r.Properties) > 32 {
				return nil, fmt.Errorf("invalid or ambiguous resource in deployment %s", d.Name)
			}
			for field := range r.Properties {
				if !serviceKeyPattern.MatchString(field) {
					return nil, fmt.Errorf("invalid property name in deployment %s", d.Name)
				}
			}
			keys[r.Key] = true
			ids[r.Service+"/"+r.ID] = true
		}
		byName[d.Name] = d
	}
	selected := byName
	if selection != "" {
		selected = map[string]serviceDeployment{}
		for _, name := range strings.Split(selection, ",") {
			name = strings.TrimSpace(name)
			d, ok := byName[name]
			if !ok {
				return nil, fmt.Errorf("selected deployment does not exist")
			}
			if _, ok := selected[name]; ok {
				return nil, fmt.Errorf("duplicate selected deployment")
			}
			selected[name] = d
		}
		if len(selected) < 2 {
			return nil, fmt.Errorf("select at least two deployments")
		}
	}
	var out []serviceDeployment
	for _, name := range sortedServiceKeys(selected) {
		out = append(out, selected[name])
	}
	return out, nil
}

func compareServiceDeployments(ds []serviceDeployment, catalog serviceCatalog, age time.Duration, showValues bool, env string, now time.Time) (finding.Report, error) {
	r := finding.Report{CompletedChecks: []string{"Comparison covers supplied logical roles and normalized field contracts only. Matching settings do not establish security, feature, cost, performance or deployment parity. Evidence is caller-supplied; no cloud commands are executed."}}
	index, err := validateServiceCatalog(catalog)
	if err != nil {
		return r, err
	}
	resources := map[string]map[string]serviceResource{}
	ready := map[string]bool{}
	roles := map[string]bool{}
	valid := map[string]map[string]bool{}
	for _, d := range ds {
		start := len(r.IncompleteChecks)
		if d.Complete == nil || !*d.Complete {
			r.IncompleteChecks = append(r.IncompleteChecks, d.Name+": collector coverage is incomplete or unknown")
		}
		if !operationLabel(d.Source) {
			r.IncompleteChecks = append(r.IncompleteChecks, d.Name+": observation source is missing")
		}
		checkFresh(&r, d.Name, d.CollectedAt, age, now)
		ready[d.Name] = start == len(r.IncompleteChecks)
		resources[d.Name] = map[string]serviceResource{}
		valid[d.Name] = map[string]bool{}
		r.CompletedChecks = append(r.CompletedChecks, "Selected deployment "+d.Name+"; cloud "+d.Cloud+"; account and location recorded in source (values withheld)")
		for _, v := range d.Resources {
			roles[v.Key] = true
			resources[d.Name][v.Key] = v
			f, ok := index[d.Cloud+"/"+v.Service]
			if !ok {
				r.IncompleteChecks = append(r.IncompleteChecks, d.Name+"/"+v.Key+": service has no mapping: "+d.Cloud+"/"+v.Service)
				continue
			}
			start := len(r.IncompleteChecks)
			for _, name := range sortedServiceKeys(f.Fields) {
				if !serviceValueValid(v.Properties[name], f.Fields[name]) {
					r.IncompleteChecks = append(r.IncompleteChecks, d.Name+"/"+v.Key+": field "+name+" is missing, null or incompatible with the mapped type/unit contract")
				}
			}
			for _, name := range sortedKeys(v.Properties) {
				if _, ok := f.Fields[name]; !ok {
					r.IncompleteChecks = append(r.IncompleteChecks, d.Name+"/"+v.Key+": unmapped property "+name+"; extend the catalog explicitly")
				}
			}
			valid[d.Name][v.Key] = start == len(r.IncompleteChecks)
		}
	}
	if len(roles) == 0 {
		r.IncompleteChecks = append(r.IncompleteChecks, "No logical resources supplied; no comparison can be established")
		return r, nil
	}
	if len(roles)*len(ds)*(len(ds)-1)/2*32 > 100000 {
		return r, fmt.Errorf("comparison exceeds 100000 possible field checks; split the deployment bundle")
	}
	roleNames := sortedServiceKeys(roles)
	for i, a := range ds {
		for _, b := range ds[i+1:] {
			pair := a.Name + " vs " + b.Name
			matching, different, missing, unknown := 0, 0, 0, 0
			for _, role := range roleNames {
				x, xok := resources[a.Name][role]
				y, yok := resources[b.Name][role]
				if !xok && !yok {
					continue
				}
				if !ready[a.Name] || !ready[b.Name] {
					unknown++
					continue
				}
				resource := a.Name + "/" + b.Name + "/" + role
				if !xok || !yok {
					missing++
					absent := a.Name
					if xok {
						absent = b.Name
					}
					addIAC(&r, "SERVICE-MISSING", finding.SeverityWarning, "Logical resource missing from deployment", resource, "compare", env, pair+"; role "+role+" is absent from "+absent+" within the declared complete scope")
					continue
				}
				fx, xmapped := index[a.Cloud+"/"+x.Service]
				fy, ymapped := index[b.Cloud+"/"+y.Service]
				if !xmapped || !ymapped {
					unknown++
					continue
				}
				if fx.ID != fy.ID {
					different++
					addIAC(&r, "SERVICE-FAMILY", finding.SeverityWarning, "Logical role uses different service families", resource, "compare", env, pair+"; role "+role+"; "+fx.ID+" versus "+fy.ID+". Fields were not treated as equivalent.")
					continue
				}
				if !valid[a.Name][role] || !valid[b.Name][role] {
					unknown++
					continue
				}
				changed := false
				for _, name := range sortedServiceKeys(fx.Fields) {
					field := fx.Fields[name]
					if equalServiceValue(x.Properties[name], y.Properties[name], field) {
						continue
					}
					changed = true
					detail := pair + "; role " + role + "; family " + fx.ID + "; field " + name + " differs"
					if field.Unit != "" {
						detail += "; unit " + safeReportText(field.Unit)
					}
					if showValues {
						left, _ := json.Marshal(x.Properties[name])
						right, _ := json.Marshal(y.Properties[name])
						detail += "; " + a.Name + "=" + safeReportText(string(left)) + "; " + b.Name + "=" + safeReportText(string(right))
					} else {
						detail += "; values withheld (use --values to reveal)"
					}
					addIAC(&r, "SERVICE-DIFF", finding.SeverityWarning, "Mapped deployment setting differs", resource+"/"+name, "compare", env, detail)
				}
				if changed {
					different++
				} else {
					matching++
					r.CompletedChecks = append(r.CompletedChecks, pair+"; role "+role+"; family "+fx.ID+": all "+strconv.Itoa(len(fx.Fields))+" mapped fields match")
				}
			}
			r.CompletedChecks = append(r.CompletedChecks, fmt.Sprintf("Pair %s: %d matching roles; %d different; %d missing; %d unknown.", pair, matching, different, missing, unknown))
		}
	}
	return r, nil
}

func runServiceCompare(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	var maps, selection string
	var age time.Duration
	var values bool
	_, o, err := parseFlags("service-compare", args, stderr, func(fs *flag.FlagSet) *commonOptions {
		var o commonOptions
		addCommonFlags(fs, &o)
		addProvenanceFlags(fs, &o)
		fs.StringVar(&maps, "maps", "", "custom service catalog JSON; replaces built-in mappings")
		fs.StringVar(&selection, "deployments", "", "comma-separated deployment names; default compares all pairs")
		fs.DurationVar(&age, "max-age", 15*time.Minute, "maximum age of deployment observations")
		fs.BoolVar(&values, "values", false, "reveal differing property values; default withholds values")
		return &o
	})
	if err != nil {
		return err
	}
	if age <= 0 || o.policy != "" || maps == "-" {
		return fmt.Errorf("positive max-age required; comparison cannot be suppressed; --maps requires a named file")
	}
	c, catalogRaw, err := loadServiceCatalog(maps, nil)
	if err != nil {
		return err
	}
	raw, err := readInput(o.input, stdin)
	if err != nil {
		return err
	}
	var b serviceDeploymentBundle
	if len(raw) > 16<<20 || workflowJSON(raw, &b) != nil {
		return fmt.Errorf("deployment bundle requires strict versioned JSON, at most 16 MiB")
	}
	ds, err := selectServiceDeployments(b, selection)
	if err != nil {
		return err
	}
	r, err := compareServiceDeployments(ds, c, age, values, o.environment, time.Now().UTC())
	if err != nil {
		return err
	}
	r.CompletedChecks = append(r.CompletedChecks, "Deployment source SHA-256: "+digestBytes(raw), "Service catalog SHA-256: "+digestBytes(catalogRaw))
	if o.changeID != "" || o.commit != "" {
		bindReportSource(&r, o, "service-compare", raw)
		if maps != "" {
			p, err := filepath.Abs(maps)
			if err != nil {
				return err
			}
			r.Provenance.Artifacts = append(r.Provenance.Artifacts, finding.ProvenanceArtifact{Path: p, SHA256: digestBytes(catalogRaw)})
		}
	}
	// Stable ordering across input deployment/resource order and map iteration.
	sort.Strings(r.IncompleteChecks)
	return emitReportOptions(stdout, o, r)
}
