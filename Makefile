test: dependencies
	go test -count 1 -v -race ./...

serve:
	go run cmd/netsync/main.go :8000

test_python:
	goreman -f Procfile start

dependencies:
	go get -t -v ./...

all:
	go run main.go
	# go run -race main.go
