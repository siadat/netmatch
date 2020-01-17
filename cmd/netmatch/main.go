package main

import (
	"fmt"
	"net/http"
	"os"

	"github.com/siadat/netmatch"
)

func main() {
	var httpAddr = os.Args[1]
	nm := netmatch.NewNetmatch()
	defer nm.Close()

	fmt.Printf("Listening for HTTP requests on %s\n", httpAddr)
	err := http.ListenAndServe(httpAddr, nm.NewHTTPHandler())
	if err != nil {
		panic(err)
	}
}
