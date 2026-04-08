.PHONY: run build install tidy

APP=depscanner

run:
	go run ./cmd/$(APP)

build:
	go build -o bin/$(APP) ./cmd/$(APP)

install:
	go install ./cmd/$(APP)

tidy:
	go mod tidy
