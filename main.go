package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"strconv"
	"sync"
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

type EventActorMessage struct {
	RID       string          `json:"-"`
	Event     string          `json:"-"`
	Actor     string          `json:"-"`
	OutChan   chan []byte     `json:"-"`
	Context   context.Context `json:"-"`
	GroupSize int             `json:"group_size"`
	InValue   string          `json:"in_value"`
	CreatedAt time.Time       `json:"created_at"`
}

type EventToActorToRequestMap struct {
	Map map[string]map[string]map[string]EventActorMessage
	mu  *sync.RWMutex
}

func main() {
	var addr = ":8080"
	fmt.Printf("Listening on %s\n", addr)

	var pendingCounter int32
	eventRequestChan := make(chan EventActorMessage)
	eventToActorToRequestMap := EventToActorToRequestMap{
		Map: make(map[string]map[string]map[string]EventActorMessage),
		mu:  &sync.RWMutex{},
	}

	cleanChan := make(chan string)

	go func() {
		for {
			completedRID := <-cleanChan
			cleanMap(eventToActorToRequestMap, completedRID)
		}
	}()

	go func() {
		for eventReq := range eventRequestChan {
			atomic.AddInt32(&pendingCounter, 1)
			fmt.Println(newLog(eventReq, "+", atomic.LoadInt32(&pendingCounter)))

			go func(eventReq EventActorMessage) {
				<-eventReq.Context.Done()
				atomic.AddInt32(&pendingCounter, -1)
				fmt.Println(newLog(eventReq, "-", atomic.LoadInt32(&pendingCounter)))
			}(eventReq)

			requests, ok := func(eventReq EventActorMessage) ([]EventActorMessage, bool) {
				requests := []EventActorMessage{eventReq}
				maxGroupSize := eventReq.GroupSize

				eventToActorToRequestMap.mu.RLock()
				defer eventToActorToRequestMap.mu.RUnlock()

				if actorToRequestMap, ok := eventToActorToRequestMap.Map[eventReq.Event]; ok {
					for actorName, actorPendingReqs := range actorToRequestMap {
						if actorName == eventReq.Actor {
							continue
						}

						for _, pendingReq := range actorPendingReqs {
							select {
							case <-pendingReq.Context.Done():
								continue
							default:
								requests = append(requests, pendingReq)
								if maxGroupSize < pendingReq.GroupSize {
									maxGroupSize = pendingReq.GroupSize
								}
							}

							if len(requests) == maxGroupSize {
								return requests, true
							}
						}
					}
				}
				return []EventActorMessage{}, false
			}(eventReq)

			if ok {
				outValue := map[string]string{}

				for _, req := range requests {
					outValue[req.Actor] = req.InValue
				}

				jsonByts := mustMarshalJson(outValue)

				for _, req := range requests {
					select {
					case req.OutChan <- jsonByts:
					case <-req.Context.Done():
					}
				}

				continue
			}

			// if we are here, it means no other actor was
			// listening, or eventToActorToRequestMap has not
			// cleaned up cancelled or done requests yet.

			func() {
				eventToActorToRequestMap.mu.Lock()
				defer eventToActorToRequestMap.mu.Unlock()

				if _, ok := eventToActorToRequestMap.Map[eventReq.Event]; !ok {
					eventToActorToRequestMap.Map[eventReq.Event] = map[string]map[string]EventActorMessage{}
				}
				if _, ok := eventToActorToRequestMap.Map[eventReq.Event][eventReq.Actor]; !ok {
					eventToActorToRequestMap.Map[eventReq.Event][eventReq.Actor] = map[string]EventActorMessage{}
				}
				eventToActorToRequestMap.Map[eventReq.Event][eventReq.Actor][eventReq.RID] = eventReq

				go func(eventReq EventActorMessage) {
					<-eventReq.Context.Done()
					cleanChan <- eventReq.RID
				}(eventReq)
			}()

		}
	}()

	http.HandleFunc("/stats", func(rw http.ResponseWriter, r *http.Request) {
		eventToActorToRequestMap.mu.RLock()
		defer eventToActorToRequestMap.mu.RUnlock()

		rw.Write(mustMarshalJson(eventToActorToRequestMap.Map))
		rw.Write([]byte("\n"))
	})

	http.HandleFunc("/event", func(rw http.ResponseWriter, r *http.Request) {
		rid := RandStringRunes(8)
		event := r.URL.Query().Get("event")
		actor := r.URL.Query().Get("actor")
		value := r.URL.Query().Get("value")
		size := 2

		if r.URL.Query().Get("size") != "" {
			var err error
			size, err = strconv.Atoi(r.URL.Query().Get("size"))
			if err != nil {
				rw.Write([]byte("size must be int\n"))
				return
			}
		}

		readyChan := make(chan []byte)
		eventRequestChan <- EventActorMessage{
			RID:       rid,
			Event:     event,
			Actor:     actor,
			OutChan:   readyChan,
			InValue:   value,
			CreatedAt: time.Now(),
			Context:   r.Context(),
			GroupSize: size,
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

func cleanMap(eventToActorToRequestMap EventToActorToRequestMap, toBeCleanedRID string) {
	eventToActorToRequestMap.mu.Lock()
	defer eventToActorToRequestMap.mu.Unlock()

	for _, actorToRequestMap := range eventToActorToRequestMap.Map {
		for actorName, pendingRequests := range actorToRequestMap {
			for rid, eventReq := range pendingRequests {
				if rid == toBeCleanedRID {
					delete(pendingRequests, rid)
					if len(pendingRequests) == 0 {
						delete(actorToRequestMap, actorName)
					}
					if len(actorToRequestMap) == 0 {
						delete(eventToActorToRequestMap.Map, eventReq.Event)
					}
					return
				}
			}
		}
	}

}

var letterRunes = []rune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ")

func newLog(eventReq EventActorMessage, msg string, pendingCounter int32) string {
	return string(mustMarshalJson(LogEvent{
		RID:     eventReq.RID,
		Actor:   eventReq.Actor,
		Event:   eventReq.Event,
		Pending: pendingCounter,
		Msg:     msg,
		Time:    time.Now(),
		Value:   eventReq.InValue,
		Age:     time.Since(eventReq.CreatedAt).Seconds(),
	}))
}

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

func mustMarshalJson(v interface{}) []byte {
	byts, err := json.Marshal(v)
	check(err)
	return byts
}
