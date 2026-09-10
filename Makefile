.PHONY: build install test vet verify

ALIASES = ansible-watch kube-explain changes collect doctor report-read fleet-check runbook incident resource-walk context-summary log-read markdown-view to-markdown spacelift-watch iac-config-check command-gen plan-explain plan-diff iac-validate iac-context spacelift-runs spacelift-diff context handoff watch a11y-output-check ai-assist ansible-check ansible2ali ansible2aws cloud-context-check config-diff config-explain config-remove config-set config-walk receipt-review decompose deploy-review diff-walk error-explain evidence-pack gha-tool git-danger-check git-diff-walker git-isimportant-check git-review git-tools git-update-json hcl2ali hcl2aws jsonprobe-check ops-policy-check pr-manager repo-policy-check review-brief review-change review-session runbook-check spacelift-check tofu-check

build:
	mkdir -p dist
	go build -o dist/rcdo ./cmd/rcdo
	@for tool in $(ALIASES); do ln -sf rcdo dist/$$tool; done

test:
	go test ./...

vet:
	go vet ./...

verify: test vet

PREFIX ?= /usr/local

install: build
	install -d $(DESTDIR)$(PREFIX)/bin
	install -m 0755 dist/rcdo $(DESTDIR)$(PREFIX)/bin/rcdo
	@for tool in $(ALIASES); do ln -sf rcdo $(DESTDIR)$(PREFIX)/bin/$$tool; done
