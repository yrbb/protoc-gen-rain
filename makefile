COMMIT_HASH=$(shell git rev-parse --short HEAD || echo "GitNotFound")
COMPILE=$(shell date '+%Y-%m-%d %H:%M:%S') by $(shell go version)
LDFLAGS="-X \"github.com/yrbb/rain.Version=${COMMIT_HASH}\" -X \"github.com/yrbb/rain.Compile=$(COMPILE)\""

.PHONY: all install build-docker
all: install

install:
	go install .

build-docker:
	docker build -f docker/dockerfile.generator -t "hub.docker.com/yy131728/raingen:v0.0.1" .
	docker push yy131728/raingen:v0.0.1
