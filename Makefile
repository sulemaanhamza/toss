.PHONY: build clean all

VERSION ?= dev

build:
	go build -ldflags "-X main.version=$(VERSION)" -o toss .

all: clean
	@mkdir -p dist
	GOOS=darwin  GOARCH=arm64 go build -o dist/toss-darwin-arm64 .
	GOOS=darwin  GOARCH=amd64 go build -o dist/toss-darwin-amd64 .
	GOOS=linux   GOARCH=amd64 go build -o dist/toss-linux-amd64 .
	GOOS=linux   GOARCH=arm64 go build -o dist/toss-linux-arm64 .
	GOOS=windows GOARCH=amd64 go build -o dist/toss-windows-amd64.exe .
	GOOS=windows GOARCH=arm64 go build -o dist/toss-windows-arm64.exe .
	@echo "done: dist/"

clean:
	rm -rf dist/ toss
