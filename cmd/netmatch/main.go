package main

import (
	"fmt"
	"net/http"
	"os"

	"github.com/siadat/netmatch"
)

func main() {
	var addr = os.Args[1]
	fmt.Printf("Listening on %s\n", addr)

	err := http.ListenAndServe(addr, netmatch.NewNetmatch().NewHandler())
	if err != nil {
		panic(err)
	}
}
