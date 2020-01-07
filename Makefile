test:
	go test -count 1 -race ./... 

run:
	goreman -f Procfile start

all:
	go run main.go
	# go run -race main.go
