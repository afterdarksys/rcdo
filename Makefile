.PHONY: build install test vet verify

ALIASES = a11y-output-check ansible-check cloud-context-check deploy-review evidence-pack gha-tool git-danger-check git-isimportant-check git-update-json pr-manager review-brief runbook-check spacelift-check tofu-check

build:
	mkdir -p dist
	go build -o dist/git-tools ./cmd/git-tools
	go build -o dist/git-diff-walker ./cmd/git-diff-walker
	@for tool in $(ALIASES); do ln -sf git-tools dist/$$tool; done

test:
	go test ./...

vet:
	go vet ./...

verify: test vet

PREFIX ?= /usr/local

install: build
	install -d $(DESTDIR)$(PREFIX)/bin
	install -m 0755 dist/git-tools $(DESTDIR)$(PREFIX)/bin/git-tools
	install -m 0755 dist/git-diff-walker $(DESTDIR)$(PREFIX)/bin/git-diff-walker
	@for tool in $(ALIASES); do ln -sf git-tools $(DESTDIR)$(PREFIX)/bin/$$tool; done
