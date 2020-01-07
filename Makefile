test:
	go test -count 1 -race ./... 

serve:
	go run cmd/syncnet/main.go

test_python:
	goreman -f Procfile start

all:
	go run main.go
	# go run -race main.go
