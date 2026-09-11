package toolkit

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"git-tools/finding"
)

type kubeMeta struct {
	Name       string `json:"name"`
	Namespace  string `json:"namespace"`
	UID        string `json:"uid"`
	Generation int64  `json:"generation,omitempty"`
}
type kubeCondition struct {
	Type   string `json:"type"`
	Status string `json:"status"`
	Reason string `json:"reason,omitempty"`
}
type kubeContainerStatus struct {
	Name         string `json:"name"`
	Ready        bool   `json:"ready"`
	RestartCount int    `json:"restartCount"`
	State        struct {
		Running *struct{} `json:"running,omitempty"`
		Waiting *struct {
			Reason string `json:"reason"`
		} `json:"waiting,omitempty"`
		Terminated *struct {
			ExitCode int    `json:"exitCode"`
			Reason   string `json:"reason"`
		} `json:"terminated,omitempty"`
	} `json:"state"`
}
type kubePod struct {
	Metadata kubeMeta `json:"metadata"`
	Spec     struct {
		Containers []struct {
			Name string `json:"name"`
		} `json:"containers"`
	} `json:"spec"`
	Status struct {
		Phase          string                `json:"phase"`
		Conditions     []kubeCondition       `json:"conditions"`
		Containers     []kubeContainerStatus `json:"containerStatuses"`
		InitContainers []kubeContainerStatus `json:"initContainerStatuses,omitempty"`
	} `json:"status"`
}
type kubeDeployment struct {
	Metadata kubeMeta `json:"metadata"`
	Spec     struct {
		Replicas *int `json:"replicas"`
	} `json:"spec"`
	Status *struct {
		ObservedGeneration int64           `json:"observedGeneration"`
		Replicas           int             `json:"replicas"`
		UpdatedReplicas    int             `json:"updatedReplicas"`
		AvailableReplicas  int             `json:"availableReplicas"`
		Conditions         []kubeCondition `json:"conditions"`
	} `json:"status"`
}
type kubeEvent struct {
	Metadata       kubeMeta `json:"metadata"`
	InvolvedObject struct {
		UID       string `json:"uid"`
		Name      string `json:"name"`
		Kind      string `json:"kind"`
		Namespace string `json:"namespace"`
	} `json:"involvedObject"`
	Type          string `json:"type"`
	Reason        string `json:"reason"`
	Count         int    `json:"count"`
	LastTimestamp string `json:"lastTimestamp"`
}
type kubeSnapshot struct {
	SchemaVersion string            `json:"schema_version"`
	Context       string            `json:"context"`
	Namespace     string            `json:"namespace"`
	CollectedAt   string            `json:"collected_at"`
	Source        string            `json:"source"`
	Coverage      map[string]string `json:"coverage"`
	Pods          []kubePod         `json:"pods"`
	Deployments   []kubeDeployment  `json:"deployments"`
	Events        []kubeEvent       `json:"events"`
}

func kubeNativeSnapshot(contextName, namespace string) kubeSnapshot {
	s := kubeSnapshot{SchemaVersion: "1", Context: contextName, Namespace: namespace, CollectedAt: time.Now().UTC().Format(time.RFC3339Nano), Source: "kubectl read-only namespace lists; context name is a configured selector, not independently authenticated cluster identity", Coverage: map[string]string{}, Pods: []kubePod{}, Deployments: []kubeDeployment{}, Events: []kubeEvent{}}
	for _, kind := range []string{"pods", "deployments", "events"} {
		result := executeReadOnly("kubectl", "--context", contextName, "--namespace", namespace, "--request-timeout=30s", "get", kind, "--output=json", "--chunk-size=100")
		s.Coverage[kind] = "error"
		if result.err != nil {
			continue
		}
		var list struct {
			Kind       string `json:"kind"`
			APIVersion string `json:"apiVersion"`
			Metadata   struct {
				Continue string `json:"continue"`
			} `json:"metadata"`
			Items []json.RawMessage `json:"items"`
		}
		if collectionJSON(result.stdout, &list) != nil || list.Items == nil || !oneOf(list.Kind, "List", "PodList", "DeploymentList", "EventList") || len(list.Items) > 10000 {
			continue
		}
		if list.Metadata.Continue != "" {
			s.Coverage[kind] = "partial"
		} else {
			s.Coverage[kind] = "pass"
		}
		for _, item := range list.Items {
			switch kind {
			case "pods":
				var p kubePod
				if json.Unmarshal(item, &p) != nil {
					s.Coverage[kind] = "partial"
				} else {
					s.Pods = append(s.Pods, p)
				}
			case "deployments":
				var d kubeDeployment
				if json.Unmarshal(item, &d) != nil {
					s.Coverage[kind] = "partial"
				} else {
					s.Deployments = append(s.Deployments, d)
				}
			case "events":
				var e kubeEvent
				if json.Unmarshal(item, &e) != nil {
					s.Coverage[kind] = "partial"
				} else {
					s.Events = append(s.Events, e)
				}
			}
		}
	}
	return s
}
func explainKube(s kubeSnapshot, contextName, namespace string, age time.Duration) (finding.Report, error) {
	r := finding.Report{CompletedChecks: []string{"Kubernetes object conditions are observations, not a diagnosis of application correctness", "Event messages, container environment and raw logs are withheld; no remediation is executed"}}
	if s.SchemaVersion != "1" || !operationLabel(s.Context) || !operationLabel(s.Namespace) || s.Pods == nil || s.Deployments == nil || s.Events == nil || len(s.Pods)+len(s.Deployments)+len(s.Events) > 10000 {
		return r, fmt.Errorf("invalid or oversized Kubernetes snapshot")
	}
	if s.Context != contextName || s.Namespace != namespace {
		addIAC(&r, "KUBE-SCOPE", finding.SeverityCritical, "Snapshot context or namespace differs from requested scope", s.Namespace, "inspect", "unknown", "Requested context/namespace must match the artifact; object analysis was skipped")
		return r, nil
	}
	checkFresh(&r, "Kubernetes snapshot", s.CollectedAt, age, time.Now().UTC())
	if !operationLabel(s.Source) {
		r.IncompleteChecks = append(r.IncompleteChecks, "Snapshot provenance missing")
	}
	for _, kind := range []string{"pods", "deployments", "events"} {
		if s.Coverage[kind] != "pass" {
			r.IncompleteChecks = append(r.IncompleteChecks, "Kubernetes "+kind+" collection missing or incomplete")
		}
	}
	ids := map[string]string{}
	names := map[string]bool{}
	meta := func(m kubeMeta, kind string) bool {
		key := kind + "/" + m.Name
		if !operationLabel(m.Name) || markdownSafe(m.Name) != m.Name || !operationLabel(m.UID) || markdownSafe(m.UID) != m.UID || m.Namespace != namespace || names[key] || ids[m.UID] != "" {
			r.IncompleteChecks = append(r.IncompleteChecks, "Invalid, duplicate or out-of-scope "+kind+" identity")
			return false
		}
		names[key] = true
		ids[m.UID] = key
		return true
	}
	add := func(prefix string, severity finding.Severity, title, resource, evidence, next string) {
		f := makeFinding(stableFindingID(&r, prefix, resource, title), severity, title, resource, "inspect", namespace, safeReportText(evidence), safeReportText(evidence), next)
		r.Findings = append(r.Findings, f)
	}
	for _, p := range s.Pods {
		if !meta(p.Metadata, "pod") {
			continue
		}
		resource := "pod/" + p.Metadata.Name
		switch p.Status.Phase {
		case "Pending":
			add("KUBE-PENDING", finding.SeverityWarning, "Pod is pending", resource, "Scheduling or startup has not completed; inspect conditions and correlated events", "Inspect PodScheduled and waiting-container reasons")
		case "Failed":
			add("KUBE-FAILED", finding.SeverityHigh, "Pod phase is Failed", resource, "Kubernetes reports terminal pod failure", "Inspect termination reasons and workload controller")
		case "Unknown", "":
			r.IncompleteChecks = append(r.IncompleteChecks, "Pod phase unavailable: "+resource)
		case "Running", "Succeeded":
		default:
			r.IncompleteChecks = append(r.IncompleteChecks, "Unsupported pod phase: "+resource)
		}
		ready := false
		readySeen := false
		for _, c := range p.Status.Conditions {
			if c.Type == "Ready" {
				readySeen = true
				ready = c.Status == "True"
			}
			if c.Status == "False" && oneOf(c.Type, "PodScheduled", "Initialized") {
				add("KUBE-CONDITION", finding.SeverityWarning, "Pod condition not satisfied: "+safeReportText(c.Type), resource, "Reason: "+c.Reason, "Inspect the corresponding condition and events")
			}
		}
		if p.Status.Phase == "Running" && !ready {
			add("KUBE-READY", finding.SeverityWarning, "Running pod is not Ready", resource, "Running phase alone does not imply readiness", "Inspect readiness probes and readiness gates")
			if !readySeen {
				r.IncompleteChecks = append(r.IncompleteChecks, "Ready condition missing: "+resource)
			}
		}
		statuses := map[string]bool{}
		for _, c := range p.Status.Containers {
			if !operationLabel(c.Name) || statuses[c.Name] {
				r.IncompleteChecks = append(r.IncompleteChecks, "Missing or duplicate container identity: "+resource)
				continue
			}
			statuses[c.Name] = true
			if p.Status.Phase == "Running" && !c.Ready {
				add("KUBE-CONTAINER-READY", finding.SeverityWarning, "Container is not ready", resource+"/"+safeReportText(c.Name), "Container ready flag is false", "Inspect readiness probes and container state")
			}
		}
		if p.Status.Phase != "Succeeded" {
			for _, c := range p.Spec.Containers {
				if !statuses[c.Name] {
					r.IncompleteChecks = append(r.IncompleteChecks, "Container status missing: "+resource+"/"+safeReportText(c.Name))
				}
			}
			if len(p.Spec.Containers) == 0 {
				r.IncompleteChecks = append(r.IncompleteChecks, "Expected container names missing: "+resource)
			}
		}
		all := append(append([]kubeContainerStatus{}, p.Status.Containers...), p.Status.InitContainers...)
		for _, c := range all {
			if !operationLabel(c.Name) || markdownSafe(c.Name) != c.Name {
				r.IncompleteChecks = append(r.IncompleteChecks, "Invalid container name: "+resource)
				continue
			}
			target := resource + "/" + safeReportText(c.Name)
			states := 0
			if c.State.Running != nil {
				states++
			}
			if c.State.Waiting != nil {
				states++
			}
			if c.State.Terminated != nil {
				states++
			}
			if states != 1 {
				r.IncompleteChecks = append(r.IncompleteChecks, "Container state missing or inconsistent: "+target)
			}
			if c.RestartCount < 0 {
				r.IncompleteChecks = append(r.IncompleteChecks, "Invalid restart count: "+target)
			} else if c.RestartCount > 0 {
				add("KUBE-RESTART", finding.SeverityWarning, "Container has recorded restarts", target, fmt.Sprintf("Restart count: %d; cumulative count is not a current restart rate", c.RestartCount), "Inspect previous logs: kubectl --context "+literalShellQuote(contextName)+" --namespace "+literalShellQuote(namespace)+" logs "+literalShellQuote(p.Metadata.Name)+" --container "+literalShellQuote(c.Name)+" --previous --tail=100")
			}
			if c.State.Waiting != nil {
				reason := safeReportText(c.State.Waiting.Reason)
				severity := finding.SeverityWarning
				if oneOf(reason, "CrashLoopBackOff", "ImagePullBackOff", "ErrImagePull", "CreateContainerConfigError") {
					severity = finding.SeverityHigh
				}
				add("KUBE-WAIT", severity, "Container waiting: "+reason, target, "Waiting reason reported by Kubernetes: "+reason, "Inspect events; for restart loops inspect previous container logs")
			}
			if c.State.Terminated != nil && c.State.Terminated.ExitCode != 0 {
				add("KUBE-EXIT", finding.SeverityHigh, "Container terminated unsuccessfully", target, fmt.Sprintf("Exit code: %d; reason: %s", c.State.Terminated.ExitCode, c.State.Terminated.Reason), "Inspect previous container logs; do not infer the root cause from exit status alone")
			}
		}
	}
	for _, d := range s.Deployments {
		if !meta(d.Metadata, "deployment") {
			continue
		}
		resource := "deployment/" + d.Metadata.Name
		if d.Spec.Replicas == nil || *d.Spec.Replicas < 0 || d.Status == nil || d.Metadata.Generation < 1 {
			r.IncompleteChecks = append(r.IncompleteChecks, "Deployment desired/status observation missing: "+resource)
			continue
		}
		if d.Status.Replicas < 0 || d.Status.UpdatedReplicas < 0 || d.Status.AvailableReplicas < 0 || d.Status.ObservedGeneration > d.Metadata.Generation {
			r.IncompleteChecks = append(r.IncompleteChecks, "Inconsistent deployment status: "+resource)
		}
		if d.Status.ObservedGeneration < d.Metadata.Generation {
			r.IncompleteChecks = append(r.IncompleteChecks, "Deployment controller has not observed the current generation: "+resource)
		}
		if d.Status.AvailableReplicas < *d.Spec.Replicas || d.Status.UpdatedReplicas < *d.Spec.Replicas || d.Status.Replicas != *d.Spec.Replicas {
			add("KUBE-ROLLOUT", finding.SeverityWarning, "Deployment rollout has not converged", resource, fmt.Sprintf("Desired %d; total %d; updated %d; available %d", *d.Spec.Replicas, d.Status.Replicas, d.Status.UpdatedReplicas, d.Status.AvailableReplicas), "Inspect deployment conditions, ReplicaSets and pod evidence")
		}
		for _, c := range d.Status.Conditions {
			if c.Type == "Progressing" && c.Status == "False" && c.Reason == "ProgressDeadlineExceeded" {
				add("KUBE-DEADLINE", finding.SeverityHigh, "Deployment exceeded its progress deadline", resource, "Controller reports ProgressDeadlineExceeded", "Investigate failing pods before considering a rollout change")
			}
		}
	}
	for _, e := range s.Events {
		if e.Type != "Warning" {
			continue
		}
		if e.Metadata.Namespace != namespace || !operationLabel(e.Metadata.UID) {
			r.IncompleteChecks = append(r.IncompleteChecks, "Warning event identity or namespace missing")
			continue
		}
		resource, matched := ids[e.InvolvedObject.UID]
		if !matched {
			resource = "event/" + safeReportText(e.Metadata.Name)
		}
		evidence := fmt.Sprintf("Event UID %s; reason %s; count %d; last timestamp %s; matched current object UID: %t. Events may describe historical failures.", e.Metadata.UID, e.Reason, e.Count, e.LastTimestamp, matched)
		add("KUBE-EVENT", finding.SeverityWarning, "Warning event: "+safeReportText(e.Reason), resource, evidence, "Inspect this event by UID and compare with current object conditions")
	}
	r.CompletedChecks = append(r.CompletedChecks, fmt.Sprintf("Reviewed %d pods, %d deployments and %d events in context %s, namespace %s", len(s.Pods), len(s.Deployments), len(s.Events), safeReportText(s.Context), safeReportText(s.Namespace)))
	return r, nil
}
func runKubeExplain(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	var native bool
	var contextName, namespace, save string
	var age time.Duration
	_, o, err := parseFlags("kube-explain", args, stderr, func(fs *flag.FlagSet) *commonOptions {
		var o commonOptions
		addCommonFlags(fs, &o)
		fs.BoolVar(&native, "native", false, "collect namespace pods/deployments/events with kubectl")
		fs.StringVar(&contextName, "context", "", "required context selector")
		fs.StringVar(&namespace, "namespace", "", "required namespace")
		fs.StringVar(&save, "save-snapshot", "", "write a new normalized snapshot file")
		fs.DurationVar(&age, "max-age", 15*time.Minute, "maximum artifact age")
		return &o
	})
	if err != nil {
		return err
	}
	if !operationLabel(contextName) || markdownSafe(contextName) != contextName || !operationLabel(namespace) || markdownSafe(namespace) != namespace || strings.HasPrefix(contextName, "-") || strings.HasPrefix(namespace, "-") || age <= 0 || o.policy != "" || (native && o.input != "-") {
		return fmt.Errorf("explicit context/namespace, positive max-age and unsuppressed coverage required; native cannot accompany input")
	}
	var s kubeSnapshot
	if native {
		s = kubeNativeSnapshot(contextName, namespace)
	} else {
		var raw []byte
		if o.input == "-" {
			raw, err = io.ReadAll(io.LimitReader(stdin, 16<<20+1))
		} else {
			raw, err = readConfigSource(o.input)
		}
		if err != nil {
			return err
		}
		if len(raw) > 16<<20 || strictJSON(raw, &s) != nil {
			return fmt.Errorf("invalid normalized Kubernetes snapshot")
		}
	}
	r, err := explainKube(s, contextName, namespace, age)
	if err != nil {
		return err
	}
	if save != "" {
		data, e := json.MarshalIndent(s, "", "  ")
		if e != nil {
			return e
		}
		if len(data) > 16<<20 {
			return fmt.Errorf("snapshot exceeds 16 MiB")
		}
		if e = publishMarkdown(save, append(data, '\n')); e != nil {
			return e
		}
		r.CompletedChecks = append(r.CompletedChecks, "Normalized snapshot saved; SHA-256: "+digestBytes(append(data, '\n')))
		o.input = save
		bindReportSource(&r, o, "kube-explain", append(data, '\n'))
	} else if native {
		data, e := json.Marshal(s)
		if e != nil {
			return e
		}
		bindReportSource(&r, o, "kube-explain", data)
	}
	return emitReportOptions(stdout, o, r)
}
