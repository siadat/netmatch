package main

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"sync"
	"sync/atomic"
)

type LogEvent struct {
	RID     string   `json:"rid"`
	Event   string   `json:"event"`
	Who     []string `json:"who"`
	Pending int32    `json:"pending"`
	Msg     string   `json:"msg"`
}

func LogEventString(e LogEvent) string {
	byts, err := json.Marshal(e)
	check(err)
	return string(byts)
}

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
	// state := []int{}

	http.HandleFunc("/spec", func(rw http.ResponseWriter, r *http.Request) {
		rid := RandStringRunes(4)
		event := r.URL.Query().Get("event")
		ch := getChan(chanCollection, &chanCollectionLock, event)
		var value string
		isUnavailable := false
		// this select is just a test to make sure we
		// print logs if it is unavailable.
		select {
		case value = <-ch:
		default:
			isUnavailable = true
		}

		if isUnavailable {
			message := "-Done"
			who := []string{"Spec"}
			atomic.AddInt32(&pendingCounter, 1)
			fmt.Println(LogEventString(LogEvent{
				RID:     rid,
				Who:     who,
				Event:   event,
				Pending: atomic.LoadInt32(&pendingCounter),
				Msg:     "+Waiting",
			}))
			defer func() {
				fmt.Println(LogEventString(LogEvent{
					RID:     rid,
					Who:     who,
					Event:   event,
					Pending: atomic.LoadInt32(&pendingCounter),
					Msg:     message,
				}))
			}()
			defer atomic.AddInt32(&pendingCounter, -1)

			select {
			case value = <-ch:
				who = []string{"Impl", "Spec"}
			case <-r.Context().Done():
				message = "-Cancelled"
			}
		}

		rw.Write([]byte(value + "\n"))
	})

	http.HandleFunc("/impl", func(rw http.ResponseWriter, r *http.Request) {
		rid := RandStringRunes(4)
		event := r.URL.Query().Get("event")
		value := r.URL.Query().Get("value")
		ch := getChan(chanCollection, &chanCollectionLock, event)
		isUnavailable := false
		// this select is just a test to make sure we print
		// logs if it is unavailable.
		select {
		case ch <- value:
		default:
			isUnavailable = true
		}

		if isUnavailable {
			message := "-Done"
			who := []string{"Impl"}
			atomic.AddInt32(&pendingCounter, 1)
			fmt.Println(LogEventString(LogEvent{
				RID:     rid,
				Who:     who,
				Event:   event,
				Pending: atomic.LoadInt32(&pendingCounter),
				Msg:     "+Waiting",
			}))
			defer func() {
				fmt.Println(LogEventString(LogEvent{
					RID:     rid,
					Who:     who,
					Event:   event,
					Pending: atomic.LoadInt32(&pendingCounter),
					Msg:     message,
				}))
			}()
			defer atomic.AddInt32(&pendingCounter, -1)

			select {
			case ch <- value:
				who = []string{"Impl", "Spec"}
			case <-r.Context().Done():
				message = "-Cancelled"
			}
		}
	})

	err := http.ListenAndServe(addr, nil)
	check(err)
}

var letterRunes = []rune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ")

func RandStringRunes(n int) string {
	b := make([]rune, n)
	for i := range b {
		b[i] = letterRunes[rand.Intn(len(letterRunes))]
	}
	return string(b)
}

func check(err error) {
	if err != nil {
		panic(err)
	}
}

func getChan(chanCollection map[string]chan string, chanCollectionLock *sync.RWMutex, event string) chan string {
	chanCollectionLock.Lock()
	ch, ok := chanCollection[event]
	if !ok {
		ch = make(chan string)
		chanCollection[event] = ch

	}
	chanCollectionLock.Unlock()
	return ch
}
