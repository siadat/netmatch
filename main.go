package main

import (
	"fmt"
	"math/rand"
	"net/http"
	"sync"
	"sync/atomic"
)

type Event struct {
	Chan   chan string
	LogIn  sync.Once
	LogOut sync.Once
}

func main() {
	var addr = ":8080" // os.Args[1]
	fmt.Println("Routes:")
	fmt.Println("  GET /spec?event=xxx")
	fmt.Println("  GET /impl?event=xxx&value=yyy")
	fmt.Printf("Listening on %s\n", addr)

	chanCollection := map[string](chan string){}
	chanCollectionLock := sync.RWMutex{}
	var pendingCounter int32

	http.HandleFunc("/spec", func(rw http.ResponseWriter, r *http.Request) {
		rid := RandStringRunes(8)
		event := r.URL.Query().Get("event")

		ch := getChan(chanCollection, &chanCollectionLock, event)
		var value string

		isUnavailable := false
		// this select is just a test to make sure we print logs if it is unavailable
		select {
		case value = <-ch:
		default:
			isUnavailable = true
		}

		if isUnavailable {
			message := "Allowed"

			atomic.AddInt32(&pendingCounter, 1)
			fmt.Printf("[%s Spec] Expecting %s (pending=%d)\n", rid, event, atomic.LoadInt32(&pendingCounter))
			defer func() {
				fmt.Printf("[%s Spec] %s %s (pending=%d)\n", rid, message, event, atomic.LoadInt32(&pendingCounter))
			}()
			defer atomic.AddInt32(&pendingCounter, -1)

			select {
			case value = <-ch:
			case <-r.Context().Done():
				message = "Cancelled"
			}
		}
		rw.Write([]byte(value + "\n"))
	})

	http.HandleFunc("/impl", func(rw http.ResponseWriter, r *http.Request) {
		rid := RandStringRunes(8)
		event := r.URL.Query().Get("event")
		value := r.URL.Query().Get("value")

		ch := getChan(chanCollection, &chanCollectionLock, event)

		isUnavailable := false
		// this select is just a test to make sure we print logs if it is unavailable
		select {
		case ch <- value:
		default:
			isUnavailable = true
		}
		if isUnavailable {
			message := "Received permission"

			atomic.AddInt32(&pendingCounter, 1)
			fmt.Printf("[%s Impl] Asking permission for %s (pending=%d)\n", rid, event, atomic.LoadInt32(&pendingCounter))
			defer func() {
				fmt.Printf("[%s Impl] %s for %s (pending=%d)\n", rid, message, event, atomic.LoadInt32(&pendingCounter))
			}()
			defer atomic.AddInt32(&pendingCounter, -1)

			select {
			case ch <- value:
			case <-r.Context().Done():
				message = "Cancelled"
				return
			}

		}
	})

	err := http.ListenAndServe(addr, nil)
	requireNoError(err)
}

var letterRunes = []rune("ABCDEFGHIJKLMNOPQRSTUVWXYZ")

func RandStringRunes(n int) string {
	b := make([]rune, n)
	for i := range b {
		b[i] = letterRunes[rand.Intn(len(letterRunes))]
	}
	return string(b)
}

func requireNoError(err error) {
	if err != nil {
		panic(err)
	}
}

func getChan(chanCollection map[string]chan string, chanCollectionLock *sync.RWMutex, event string) chan string {
	// fmt.Printf("spec chanCollection = %+v\n", chanCollection)
	chanCollectionLock.Lock()
	ch, ok := chanCollection[event]
	if !ok {
		ch = make(chan string)
		chanCollection[event] = ch

	}
	chanCollectionLock.Unlock()
	return ch
}
