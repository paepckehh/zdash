PROJECT=$(shell basename $(CURDIR))
PROJECT_BASE=paepcke.de
PROJECT_HOST=github.com
PROJECT_HOST_ID=paepckehh
PROJECT_MGR_EMAIL=git@$(PROJECT_BASE)
PROJECT_MGR_NAME=PAECPCKE, Michael
PROJECT_REPO=$(PROJECT_BASE)/$(PROJECT)
PROJECT_URL_HTTPS=https://$(PROJECT_HOST)/$(PROJECT_HOST_ID)/$(PROJECT)
PROJECT_URL_SSH=git@$(PROJECT_HOST):$(PROJECT_HOST_ID)/$(PROJECT)
# Semantic version: latest git tag, or "dev" when no tags exist.
VERSION=$(shell git describe --tags --abbrev=0 2>/dev/null || echo 'dev')

all: info

info:
	echo "$(PROJECT)"

run:    build
	BIND_ADDR=0.0.0.0:8080 ./$(PROJECT)

build:
	touch $(PROJECT) && rm $(PROJECT)
	go build -C cmd/$(PROJECT) -o ../../zdash -ldflags="-X 'main.version=$(VERSION)' -X 'paepcke.de/zdash.Version=$(VERSION)'"

deps:
	touch go.mod go.sum
	rm go.mod go.sum
	go mod init $(PROJECT_BASE)/$(PROJECT)
	go mod tidy -v

check:
	CGO_ENABLED=0 go fmt ./...
	CGO_ENABLED=0 go vet ./...
	CGO_ENABLED=0 go fix ./...
	# CGO_ENABLED=0 golangci-lint run
	# CGO_ENABLED=0 staticcheck

prep-git:
	git remote set-url origin $(PROJECT_URL_HTTPS)
	git remote -v

prep-git-ssh:
	git config user.name $(PROJECT_MGR_EMAIL)
	git config user.email $(PROJECT_MGR_NAME)
	git remote set-url origin $(PROJECT_URL_SSH)
	git remote -v
