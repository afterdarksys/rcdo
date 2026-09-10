package toolkit

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"reflect"
	"sort"
	"time"

	"git-tools/finding"
)

type fleetHost struct {
	ID       string `json:"id"`
	Platform string `json:"platform"`
	Baseline string `json:"baseline"`
}
type fleetBaseline struct {
	Platform    string         `json:"platform"`
	Values      map[string]any `json:"values"`
	Source      string         `json:"source"`
	CollectedAt string         `json:"collected_at"`
}
type fleetObservation struct {
	Host        string         `json:"host"`
	Platform    string         `json:"platform"`
	Outcome     string         `json:"outcome"`
	Values      map[string]any `json:"values"`
	Source      string         `json:"source"`
	CollectedAt string         `json:"collected_at"`
}
type fleetBundle struct {
	SchemaVersion string                   `json:"schema_version"`
	Complete      *bool                    `json:"complete"`
	Hosts         []fleetHost              `json:"hosts"`
	Baselines     map[string]fleetBaseline `json:"baselines"`
	Observations  []fleetObservation       `json:"observations"`
}

func compareFleet(b fleetBundle, age, baselineAge time.Duration, now time.Time) (finding.Report, error) {
	r := finding.Report{}
	if b.SchemaVersion != "1" || b.Hosts == nil || len(b.Hosts) == 0 || len(b.Hosts) > 10000 || b.Baselines == nil || b.Observations == nil || len(b.Observations) > 10000 {
		return r, fmt.Errorf("fleet requires version 1, 1..10000 manifest hosts, baselines and observations")
	}
	hosts := map[string]fleetHost{}
	for _, h := range b.Hosts {
		if !operationLabel(h.ID) || !operationLabel(h.Platform) || !operationLabel(h.Baseline) {
			return r, fmt.Errorf("invalid manifest host")
		}
		if _, ok := hosts[h.ID]; ok {
			return r, fmt.Errorf("duplicate manifest host")
		}
		hosts[h.ID] = h
	}
	observations := map[string]fleetObservation{}
	for _, o := range b.Observations {
		if !operationLabel(o.Host) || !oneOf(o.Outcome, "pass", "unreachable", "error") {
			return r, fmt.Errorf("invalid host observation")
		}
		if _, ok := observations[o.Host]; ok {
			return r, fmt.Errorf("duplicate host observation")
		}
		observations[o.Host] = o
		if _, ok := hosts[o.Host]; !ok {
			r.IncompleteChecks = append(r.IncompleteChecks, "Observation outside required manifest: "+markdownSafe(o.Host))
		}
	}
	if b.Complete == nil || !*b.Complete {
		r.IncompleteChecks = append(r.IncompleteChecks, "Fleet collector declares incomplete coverage")
	}
	ids := make([]string, 0, len(hosts))
	for id := range hosts {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	counts := map[string]int{}
	for _, id := range ids {
		h := hosts[id]
		base, ok := b.Baselines[h.Baseline]
		if !ok {
			counts["unknown"]++
			r.IncompleteChecks = append(r.IncompleteChecks, "Baseline missing for host "+id)
			continue
		}
		if !operationLabel(base.Platform) || !operationLabel(base.Source) || len(base.Values) == 0 {
			return r, fmt.Errorf("baseline requires platform, source and nonempty values")
		}
		if base.Platform != h.Platform {
			counts["unknown"]++
			r.IncompleteChecks = append(r.IncompleteChecks, "Baseline platform incompatible for host "+id)
			continue
		}
		var freshness finding.Report
		checkFresh(&freshness, "Baseline for "+id, base.CollectedAt, baselineAge, now)
		o, ok := observations[id]
		if !ok {
			counts["missing"]++
			r.IncompleteChecks = append(r.IncompleteChecks, "Required host observation missing: "+id)
			continue
		}
		if o.Outcome != "pass" {
			counts[o.Outcome]++
			r.IncompleteChecks = append(r.IncompleteChecks, "Host "+id+": "+o.Outcome)
			continue
		}
		checkFresh(&freshness, "Host "+id, o.CollectedAt, age, now)
		if !operationLabel(o.Source) {
			freshness.IncompleteChecks = append(freshness.IncompleteChecks, "Observation source missing for "+id)
		}
		if len(freshness.IncompleteChecks) > 0 {
			counts["stale_or_unknown"]++
			r.IncompleteChecks = append(r.IncompleteChecks, freshness.IncompleteChecks...)
			continue
		}
		different, missing := false, false
		if o.Platform != h.Platform {
			different = true
			addIAC(&r, "FLE-PLATFORM", finding.SeverityHigh, "Host platform differs from manifest", id, "compare", "unknown", "Expected "+h.Platform+"; observed "+markdownSafe(o.Platform))
		}
		for _, key := range sortedKeys(base.Values) {
			if !operationLabel(key) {
				return r, fmt.Errorf("invalid baseline field")
			}
			value, exists := o.Values[key]
			if !exists {
				missing = true
				r.IncompleteChecks = append(r.IncompleteChecks, "Host "+id+" missing required field "+key)
				continue
			}
			if !reflect.DeepEqual(value, base.Values[key]) {
				different = true
				addIAC(&r, "FLE-DIFF", finding.SeverityWarning, "Host differs from baseline", id+"/"+key, "compare", "unknown", "Field "+key+" differs from baseline "+h.Baseline+"; values withheld")
			}
		}
		if missing {
			counts["partial"]++
		}
		if different {
			counts["different"]++
		}
		if !missing && !different {
			counts["matching"]++
			r.CompletedChecks = append(r.CompletedChecks, "Host matches required baseline fields: "+id)
		}
	}
	r.CompletedChecks = append(r.CompletedChecks, fmt.Sprintf("Fleet: %d required; %d matching; %d different; %d partial; %d unreachable; %d errors; %d missing; %d stale or unknown; %d unknown baseline. Partial and different may overlap.", len(hosts), counts["matching"], counts["different"], counts["partial"], counts["unreachable"], counts["error"], counts["missing"], counts["stale_or_unknown"], counts["unknown"]), "Comparison covers supplied manifest and required baseline fields only; no live host collection or remediation")
	return r, nil
}
func runFleetCheck(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	var age, baselineAge time.Duration
	_, o, err := parseFlags("fleet-check", args, stderr, func(fs *flag.FlagSet) *commonOptions {
		var o commonOptions
		addCommonFlags(fs, &o)
		fs.DurationVar(&age, "max-age", 15*time.Minute, "maximum host observation age")
		fs.DurationVar(&baselineAge, "baseline-max-age", 24*time.Hour, "maximum baseline age")
		return &o
	})
	if err != nil {
		return err
	}
	if age <= 0 || baselineAge <= 0 || o.policy != "" {
		return fmt.Errorf("positive freshness limits required; fleet coverage cannot be suppressed")
	}
	data, err := readInput(o.input, io.LimitReader(stdin, 16<<20+1))
	if err != nil {
		return err
	}
	if len(data) > 16<<20 {
		return fmt.Errorf("fleet input exceeds 16 MiB")
	}
	var b fleetBundle
	if strictJSON(data, &b) != nil {
		return fmt.Errorf("invalid fleet JSON")
	}
	// Preserve integer precision in baseline and observation values.
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&b); err != nil {
		return err
	}
	r, err := compareFleet(b, age, baselineAge, time.Now().UTC())
	if err != nil {
		return err
	}
	return emitReportOptions(stdout, o, r)
}
