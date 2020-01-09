package main

import (
	"fmt"
	"net/http"
	"os"

	"github.com/siadat/syncnet"
)

func main() {
	var addr = os.Args[1]
	fmt.Printf("Listening on %s\n", addr)

	err := http.ListenAndServe(addr, syncnet.NewSyncnet().NewHandler())
	if err != nil {
		panic(err)
	}
}
