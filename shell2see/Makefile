.PHONY: build test
build:
	mkdir -p build
	go build -o build/textsee ./cmd/textsee

test: build
	go test ./...
	python3 -m unittest discover -s tests -v
	bash -n bin/pdfgrep
