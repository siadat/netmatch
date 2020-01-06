test:
	goreman -f Procfile start

all:
	go run main.go
	# go run -race main.go
