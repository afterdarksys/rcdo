package toolkit

import (
	"flag"
	"fmt"
	"git-tools/finding"
	"io"
	"reflect"
	"strings"
	"time"
)

// A bounded snapshot sequence works offline and preserves every phase observation.
func runSpaceUtility(mode string, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	var before, after, state, kind string
	var maxAge time.Duration
	_, o, err := parseFlags(mode, args, stderr, func(fs *flag.FlagSet) *commonOptions {
		var o commonOptions
		addCommonFlags(fs, &o)
		fs.DurationVar(&maxAge, "max-age", 15*time.Minute, "maximum evidence age")
		if mode == "spacelift-diff" {
			fs.StringVar(&before, "before", "", "previous run snapshot")
			fs.StringVar(&after, "after", "", "new run snapshot")
		} else {
			fs.StringVar(&state, "state", "", "filter exact run state")
			fs.StringVar(&kind, "type", "", "filter exact run type")
		}
		return &o
	})
	if err != nil {
		return err
	}
	if maxAge <= 0 {
		return fmt.Errorf("--max-age must be positive")
	}
	r := finding.Report{CompletedChecks: []string{"Bounded Spacelift snapshot review"}, Findings: []finding.Finding{}}
	now := time.Now().UTC()
	if mode == "spacelift-diff" {
		if before == "" || after == "" || before == "-" && after == "-" {
			return fmt.Errorf("supply --before and --after; at most one may be stdin")
		}
		bd, err := readInput(before, stdin)
		if err != nil {
			return err
		}
		ad, err := readInput(after, stdin)
		if err != nil {
			return err
		}
		b, err := decodeSpace(bd)
		if err != nil {
			return err
		}
		a, err := decodeSpace(ad)
		if err != nil {
			return err
		}
		r = reviewSpace(a, spaceOptions{maxAge: maxAge}, o.environment, now)
		if b.SchemaVersion != "1" || b.RunID == "" || b.StackID == "" {
			r.IncompleteChecks = append(r.IncompleteChecks, "Previous snapshot identity unavailable")
		}
		if b.Account != a.Account || b.StackID != a.StackID {
			addIAC(&r, "SPACE-DIFF-TARGET", finding.SeverityCritical, "Run comparison crosses target identities", a.RunID, "compare", o.environment, "Account or stack differs")
		}
		for _, v := range []struct {
			name string
			b, a any
		}{{"run", b.RunID, a.RunID}, {"commit", b.CommitSHA, a.CommitSHA}, {"state", b.State, a.State}, {"type", b.RunType, a.RunType}, {"policies", b.Policies, a.Policies}, {"approval", b.Approval, a.Approval}, {"configuration", b.Config, a.Config}, {"dependencies", b.Dependencies, a.Dependencies}, {"plan binding", b.Plan, a.Plan}} {
			if !reflect.DeepEqual(v.b, v.a) {
				evidence := "Values withheld; inspect the corresponding snapshot section"
				if oneOf(v.name, "run", "commit", "state", "type") {
					evidence = fmt.Sprintf("%v -> %v", v.b, v.a)
				}
				addIAC(&r, "SPACE-DIFF", finding.SeverityWarning, v.name+" changed", a.RunID, "compare", o.environment, evidence)
			}
		}
	} else {
		data, err := readInput(o.input, stdin)
		if err != nil {
			return err
		}
		var envelope struct {
			SchemaVersion string          `json:"schema_version"`
			Complete      *bool           `json:"complete"`
			Runs          []spaceSnapshot `json:"runs"`
		}
		if err := strictJSON(data, &envelope); err != nil {
			return err
		}
		if envelope.SchemaVersion != "1" || envelope.Complete == nil || !*envelope.Complete || envelope.Runs == nil {
			r.IncompleteChecks = append(r.IncompleteChecks, "Run sequence is incomplete or has unknown schema")
		}
		if len(envelope.Runs) > 1000 {
			return fmt.Errorf("run sequence exceeds 1000 observations")
		}
		for _, s := range envelope.Runs {
			if state != "" && !strings.EqualFold(state, s.State) || kind != "" && !strings.EqualFold(kind, s.RunType) {
				continue
			}
			if s.SchemaVersion != "1" || s.Source == "" {
				r.IncompleteChecks = append(r.IncompleteChecks, "Run observation schema or source unavailable")
			}
			checkFresh(&r, "Run "+s.RunID, s.CollectedAt, maxAge, now)
			if s.RunID == "" || s.StackID == "" || s.State == "" {
				r.IncompleteChecks = append(r.IncompleteChecks, "Observation identity or state missing")
				continue
			}
			addIAC(&r, "SPACE-RUN", finding.SeverityInfo, "Run observation", s.RunID, "inspect", o.environment, "Stack: "+s.StackID+"; type: "+s.RunType+"; state: "+s.State+"; commit: "+s.CommitSHA+"; observed: "+s.CollectedAt)
		}
	}
	return emitReportOptions(stdout, o, r)
}

func runSpaceWatch(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	var stack, run string
	var samples int
	var interval time.Duration
	_, o, err := parseFlags("spacelift-watch", args, stderr, func(fs *flag.FlagSet) *commonOptions {
		var o commonOptions
		addCommonFlags(fs, &o)
		fs.StringVar(&stack, "stack", "", "stack ID")
		fs.StringVar(&run, "run", "", "run ID")
		fs.IntVar(&samples, "samples", 3, "bounded observations, 1 to 100")
		fs.DurationVar(&interval, "interval", 5*time.Second, "poll interval, 1s to 1m; total wait at most 10m")
		return &o
	})
	if err != nil {
		return err
	}
	if stack == "" || run == "" || samples < 1 || samples > 100 || interval < time.Second || interval > time.Minute || time.Duration(samples-1)*interval > 10*time.Minute || o.input != "-" {
		return fmt.Errorf("supply --stack and --run, samples 1..100, interval 1s..1m and total wait <=10m; file input is unsupported")
	}
	observations := finding.Report{CompletedChecks: []string{"Bounded polling; each observation queries the actual run; intermediate remote transitions may be missed"}}
	lastState := ""
	for i := 0; i < samples; i++ {
		if i > 0 {
			time.Sleep(interval)
		}
		s, err := collectSpace(stack, run)
		if err != nil {
			observations.IncompleteChecks = append(observations.IncompleteChecks, fmt.Sprintf("Observation %d failed; continuity gap", i+1))
			continue
		}
		if s.StackID != stack || s.RunID != run {
			addIAC(&observations, "SPACE-WATCH-TARGET", finding.SeverityCritical, "Collector returned a different target", run, "watch", o.environment, "Requested stack/run did not match observation")
			continue
		}
		if s.State != lastState {
			addIAC(&observations, "SPACE-PHASE", finding.SeverityInfo, "Run phase observation", run, "watch", o.environment, "State: "+s.State+"; observed: "+s.CollectedAt)
			lastState = s.State
			if o.format == "text" {
				writeWrapped(stdout, "Observation: "+s.State+" at "+s.CollectedAt, o.width)
			}
		}
		if oneOf(s.State, "FINISHED", "FAILED", "REJECTED", "DISCARDED", "CANCELED", "STOPPED") || i == samples-1 {
			r := reviewSpace(s, spaceOptions{stack: stack, run: run, maxAge: 15 * time.Minute}, o.environment, time.Now().UTC())
			observations.Findings = append(observations.Findings, r.Findings...)
			observations.IncompleteChecks = append(observations.IncompleteChecks, r.IncompleteChecks...)
			break
		}
	}
	if lastState == "" {
		observations.IncompleteChecks = append(observations.IncompleteChecks, "No valid run observations")
	}
	return emitReportOptions(stdout, o, observations)
}
