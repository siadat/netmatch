package main

import (
	"fmt"
	"net/http"

	"github.com/siadat/syncnet"
)

func main() {
	var addr = ":8080"
	fmt.Printf("Listening on %s\n", addr)

	err := http.ListenAndServe(addr, syncnet.NewHandler())
	if err != nil {
		panic(err)
	}
}
