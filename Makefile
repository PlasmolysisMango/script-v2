GOPROXY ?= https://goproxy.cn,direct
export GOPROXY

.PHONY: build test web run e2e
build: web
	mkdir -p bin
	go build -o bin/jbs-server ./cmd/jbs-server
	go build -o bin/jbs ./cmd/jbs

web:
	npm --prefix web ci
	npm --prefix web run build

test:
	go test -race ./...

run:
	go run ./cmd/jbs-server --static web/dist

e2e:
	go test -tags e2e ./e2e -v
