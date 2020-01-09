test: dependencies
	go test -count 1 -v -race ./...

serve:
	go run cmd/syncnet/main.go

test_python:
	goreman -f Procfile start

dependencies:
	go get -v ./...

all:
	go run main.go
	# go run -race main.go
