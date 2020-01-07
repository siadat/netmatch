package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"sync/atomic"
	"time"
)

type LogEvent struct {
	Time    time.Time `json:"time"`
	RID     string    `json:"rid"`
	Msg     string    `json:"msg"`
	Event   string    `json:"event"`
	Actor   string    `json:"actor"`
	Value   string    `json:"value"`
	Pending int32     `json:"pending"`
	Age     float64   `json:"age"`
}

func mustMarshalJson(v interface{}) []byte {
	byts, err := json.Marshal(v)
	check(err)
	return byts
}

type EventActorMessage struct {
	RID           string          `json:"-"`
	Event         string          `json:"-"`
	Actor         string          `json:"-"`
	OutValueReady chan []byte     `json:"-"`
	Context       context.Context `json:"-"`
	InValue       string          `json:"in_value"`
	CreatedAt     time.Time       `json:"created_at"`
}

func main() {
	var addr = ":8080" // os.Args[1]
	fmt.Printf("Listening on %s\n", addr)

	var pendingCounter int32

	eventRequestChan := make(chan EventActorMessage)
	eventToActorToRequestMap := map[string]map[string]map[string]EventActorMessage{}

	printState := func() {
		if true {
			return
		}
		fmt.Println("# State:")
		for event, actorToRequestMap := range eventToActorToRequestMap {
			fmt.Printf("  event = %+v\n", event)
			for actorName, pendingRequests := range actorToRequestMap {
				fmt.Printf("  actorName = %+v\n", actorName)
				for rid := range pendingRequests {
					fmt.Printf("  rid = %+v\n", rid)
				}
			}
		}
		fmt.Println("# State end.")
	}

	cleanChan := make(chan string)

	go func() {
		for {
			completedRID := <-cleanChan
			func() {
				for _, actorToRequestMap := range eventToActorToRequestMap {
					for actorName, pendingRequests := range actorToRequestMap {
						for rid, eventReq := range pendingRequests {
							if rid == completedRID {
								delete(pendingRequests, rid)
								if len(pendingRequests) == 0 {
									delete(actorToRequestMap, actorName)
								}
								if len(actorToRequestMap) == 0 {
									delete(eventToActorToRequestMap, eventReq.Event)
								}
								return
							}
						}
					}
				}
			}()
			printState()
		}
	}()

	go func() {
	REQ_LOOP:
		for {
			printState()
			eventReq := <-eventRequestChan

			atomic.AddInt32(&pendingCounter, 1)
			fmt.Println(string(mustMarshalJson(LogEvent{
				RID:     eventReq.RID,
				Actor:   eventReq.Actor,
				Event:   eventReq.Event,
				Pending: atomic.LoadInt32(&pendingCounter),
				Msg:     "+",
				Time:    time.Now(),
				Value:   eventReq.InValue,
				Age:     time.Since(eventReq.CreatedAt).Seconds(),
			})))

			go func() {
				<-eventReq.Context.Done()
				cleanChan <- eventReq.RID

				atomic.AddInt32(&pendingCounter, -1)
				fmt.Println(string(mustMarshalJson(LogEvent{
					RID:     eventReq.RID,
					Actor:   eventReq.Actor,
					Event:   eventReq.Event,
					Pending: atomic.LoadInt32(&pendingCounter),
					Msg:     "-",
					Time:    time.Now(),
					Value:   eventReq.InValue,
					Age:     time.Since(eventReq.CreatedAt).Seconds(),
				})))
			}()

			if actorToRequestMap, ok := eventToActorToRequestMap[eventReq.Event]; ok {
				for actorName, actorPendingReqs := range actorToRequestMap {
					if actorName == eventReq.Actor {
						continue
					}

					for _, pendingReq := range actorPendingReqs {
						outValue := map[string]string{}
						outValue[pendingReq.Actor] = pendingReq.InValue
						outValue[eventReq.Actor] = eventReq.InValue
						jsonByts := mustMarshalJson(outValue)

						select {
						case pendingReq.OutValueReady <- jsonByts:
						case <-pendingReq.Context.Done():
							continue
						}

						select {
						case eventReq.OutValueReady <- jsonByts:
						case <-eventReq.Context.Done():
							continue REQ_LOOP
						}

						// successfully delivered to both
						continue REQ_LOOP
					}
				}
			}

			// if we are here, it means no other actor was
			// listening, or eventToActorToRequestMap has not
			// cleaned up cancelled or done requests yet.

			if _, ok := eventToActorToRequestMap[eventReq.Event]; !ok {
				eventToActorToRequestMap[eventReq.Event] = map[string]map[string]EventActorMessage{}
			}
			if _, ok := eventToActorToRequestMap[eventReq.Event][eventReq.Actor]; !ok {
				eventToActorToRequestMap[eventReq.Event][eventReq.Actor] = map[string]EventActorMessage{}
			}
			eventToActorToRequestMap[eventReq.Event][eventReq.Actor][eventReq.RID] = eventReq

		}
	}()

	http.HandleFunc("/stats", func(rw http.ResponseWriter, r *http.Request) {
		rw.Write(mustMarshalJson(eventToActorToRequestMap))
		rw.Write([]byte("\n"))
	})

	http.HandleFunc("/event", func(rw http.ResponseWriter, r *http.Request) {
		rid := RandStringRunes(8)
		event := r.URL.Query().Get("event")
		actor := r.URL.Query().Get("actor")
		value := r.URL.Query().Get("value")

		readyChan := make(chan []byte)
		eventRequestChan <- EventActorMessage{
			RID:           rid,
			Event:         event,
			Actor:         actor,
			OutValueReady: readyChan,
			InValue:       value,
			CreatedAt:     time.Now(),
			Context:       r.Context(),
		}

		select {
		case out := <-readyChan:
			rw.Write(out)
			rw.Write([]byte("\n"))
		case <-r.Context().Done():
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
