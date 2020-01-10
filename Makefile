test: dependencies
	# -parallel 1
	go test -race -count 1 -v ./...

serve:
	go run cmd/netsync/main.go :8000

test_python:
	goreman -f Procfile start

dependencies:
	go get -t -v ./...
