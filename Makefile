PROJECT=$(shell basename $(CURDIR))

build:
	cd cmd/$(PROJECT) && go build -ldflags="-X main.version=$(git describe --tags --abbrev=0 2>/dev/null || echo 'dev')"

deps:
	touch go.mod go.sum
	rm go.mod go.sum
	go mod init paepcke.de/$(PROJECT)
	go mod tidy -v

check:
	git config user.name "PAEPCKE, Michael"
	git config user.email "git@paepcke.de"
	CGO_ENABLED=0 go fmt ./...
	CGO_ENABLED=0 go vet ./...
	CGO_ENABLED=0 go fix -diff .
	CGO_ENABLED=0 go fix .
	# CGO_ENABLED=0 golangci-lint run
	# CGO_ENABLED=0 staticcheck
