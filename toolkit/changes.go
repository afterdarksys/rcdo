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

func reportChanges(before, after finding.Report) finding.Report {
	r := finding.Report{CompletedChecks: []string{"Report comparison uses stable finding IDs. Source report coverage is not independently attested; absence is not verified recovery."}}
	for _, s := range before.IncompleteChecks {
		r.IncompleteChecks = append(r.IncompleteChecks, "Historical comparison gap: "+safeReportText(s))
	}
	for _, s := range after.IncompleteChecks {
		r.IncompleteChecks = append(r.IncompleteChecks, "Current gap: "+safeReportText(s))
	}
	old := map[string]finding.Finding{}
	for _, f := range before.Findings {
		old[f.ID] = f
	}
	current := map[string]bool{}
	for _, f := range after.Findings {
		current[f.ID] = true
		prior, ok := old[f.ID]
		state := "Newly reported"
		if ok {
			state = "Still reported"
			if !reflect.DeepEqual(prior, f) {
				state = "Changed finding"
			}
		}
		f.Title = state + ": " + f.Title
		f.Evidence = append(append([]string{}, f.Evidence...), "Compared using finding ID "+f.ID)
		r.Findings = append(r.Findings, f)
	}
	for _, f := range before.Findings {
		if !current[f.ID] {
			addIAC(&r, "CHG-ABSENT", finding.SeverityInfo, "Finding no longer reported; recovery not verified", f.Resource, f.Action, f.Environment, "Previous finding ID: "+f.ID+"; current report absence does not establish successful remediation")
		}
	}
	r.CompletedChecks = append(r.CompletedChecks, fmt.Sprintf("Before: %d findings; after: %d findings", len(before.Findings), len(after.Findings)))
	return r
}
func decodeFleetSnapshot(raw []byte) (fleetBundle, error) {
	var b fleetBundle
	if strictJSON(raw, &b) != nil {
		return b, fmt.Errorf("invalid fleet snapshot")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if err := d.Decode(&b); err != nil {
		return b, err
	}
	_, err := compareFleet(b, time.Hour, time.Hour, time.Now())
	return b, err
}
func fleetHostState(b fleetBundle, h fleetHost, age, baselineAge time.Duration) (string, finding.Report) {
	one := b
	one.Hosts = []fleetHost{h}
	one.Observations = []fleetObservation{}
	for _, o := range b.Observations {
		if o.Host == h.ID {
			one.Observations = append(one.Observations, o)
		}
	}
	r, _ := compareFleet(one, age, baselineAge, time.Now().UTC())
	if len(r.IncompleteChecks) > 0 {
		return "unknown", r
	}
	if len(r.Findings) > 0 {
		return "different", r
	}
	return "matching", r
}
func fleetChanges(before, after fleetBundle, age, baselineAge time.Duration) (finding.Report, error) {
	r := finding.Report{CompletedChecks: []string{"Fleet changes compare required fields in dated observations; matching does not certify service recovery."}}
	coverage, err := compareFleet(after, age, baselineAge, time.Now().UTC())
	if err != nil {
		return r, err
	}
	r.IncompleteChecks = append(r.IncompleteChecks, coverage.IncompleteChecks...)
	old, newHosts := map[string]fleetHost{}, map[string]fleetHost{}
	for _, h := range before.Hosts {
		old[h.ID] = h
	}
	for _, h := range after.Hosts {
		newHosts[h.ID] = h
	}
	ids := map[string]bool{}
	for id := range old {
		ids[id] = true
	}
	for id := range newHosts {
		ids[id] = true
	}
	ordered := []string{}
	for id := range ids {
		ordered = append(ordered, id)
	}
	sort.Strings(ordered)
	for _, id := range ordered {
		a, aok := old[id]
		b, bok := newHosts[id]
		if !bok {
			r.IncompleteChecks = append(r.IncompleteChecks, "Host removed from required manifest, not verified recovered: "+id)
			continue
		}
		if aok {
			ab, bb := before.Baselines[a.Baseline], after.Baselines[b.Baseline]
			if a.Platform != b.Platform || a.Baseline != b.Baseline || ab.Platform != bb.Platform || !reflect.DeepEqual(ab.Values, bb.Values) {
				return r, fmt.Errorf("host %s baseline or platform changed; compare equivalent baselines before interpreting recovery", safeReportText(id))
			}
		}
		current, details := fleetHostState(after, b, age, baselineAge)
		for _, f := range details.Findings {
			f.ID = stableFindingID(&r, "CHG-CURRENT", f.ID, f.Resource)
			r.Findings = append(r.Findings, f)
		}
		prior := "not previously required"
		if aok {
			prior, _ = fleetHostState(before, a, age, baselineAge)
		}
		title := "Host status unchanged: " + current
		if prior != current {
			title = "Host observation changed: " + prior + " to " + current
		}
		sev := finding.SeverityInfo
		if current == "different" {
			sev = finding.SeverityWarning
		}
		addIAC(&r, "CHG-HOST", sev, title, id, "compare", "unknown", "Before: "+prior+"; after: "+current+". Only required fields and declared coverage were evaluated.")
		if prior == "unknown" {
			r.CompletedChecks = append(r.CompletedChecks, "Previous host evidence was unknown: "+id+"; a newly matching observation is not proof of a recovery event")
		}
	}
	if before.Complete == nil || !*before.Complete {
		r.CompletedChecks = append(r.CompletedChecks, "Historical fleet collection was incomplete; previous states may be unknown")
	}
	if after.Complete == nil || !*after.Complete {
		r.IncompleteChecks = append(r.IncompleteChecks, "Current fleet collection is incomplete")
	}
	return r, nil
}
func runChanges(args []string, stdout, stderr io.Writer) error {
	var beforePath, afterPath, kind string
	var age, baselineAge time.Duration
	_, o, err := parseFlags("changes", args, stderr, func(fs *flag.FlagSet) *commonOptions {
		var o commonOptions
		addCommonFlags(fs, &o)
		fs.StringVar(&beforePath, "before", "", "earlier JSON artifact")
		fs.StringVar(&afterPath, "after", "", "later JSON artifact")
		fs.StringVar(&kind, "kind", "report", "report or fleet")
		fs.DurationVar(&age, "max-age", 15*time.Minute, "maximum fleet observation age")
		fs.DurationVar(&baselineAge, "baseline-max-age", 24*time.Hour, "maximum baseline age")
		return &o
	})
	if err != nil {
		return err
	}
	if beforePath == "" || afterPath == "" || !oneOf(kind, "report", "fleet") || o.input != "-" || o.policy != "" || age <= 0 || baselineAge <= 0 {
		return fmt.Errorf("changes requires before/after artifacts, report|fleet, positive freshness and unsuppressed coverage")
	}
	a, err := readConfigSource(beforePath)
	if err != nil {
		return err
	}
	b, err := readConfigSource(afterPath)
	if err != nil {
		return err
	}
	var r finding.Report
	if kind == "report" {
		if validateConfigDocument("json", a) != nil || validateConfigDocument("json", b) != nil {
			return fmt.Errorf("invalid or ambiguous report JSON")
		}
		old, e := decodeSessionReport(a)
		if e != nil {
			return e
		}
		current, e := decodeSessionReport(b)
		if e != nil {
			return e
		}
		r = reportChanges(old, current)
	} else {
		old, e := decodeFleetSnapshot(a)
		if e != nil {
			return e
		}
		current, e := decodeFleetSnapshot(b)
		if e != nil {
			return e
		}
		r, err = fleetChanges(old, current, age, baselineAge)
		if err != nil {
			return err
		}
	}
	r.CompletedChecks = append(r.CompletedChecks, "Before SHA-256: "+digestBytes(a), "After SHA-256: "+digestBytes(b))
	// Text presentation is sanitized; machine-readable evidence stays exact.
	if o.format == "text" {
		for i := range r.IncompleteChecks {
			r.IncompleteChecks[i] = safeReportText(r.IncompleteChecks[i])
		}
		for i := range r.Findings {
			f := &r.Findings[i]
			f.ID = safeReportText(f.ID)
			f.Title = safeReportText(f.Title)
			f.Resource = safeReportText(f.Resource)
			f.Reason = safeReportText(f.Reason)
			f.Action = safeReportText(f.Action)
			f.Environment = safeReportText(f.Environment)
			f.Remediation = safeReportText(f.Remediation)
			for j := range f.Evidence {
				f.Evidence[j] = safeReportText(f.Evidence[j])
			}
		}
	}
	return emitReportOptions(stdout, o, r)
}
