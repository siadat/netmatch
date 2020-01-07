package syncnet

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"k8s.io/apimachinery/pkg/labels"
)

type Params struct {
	Event      string            `json:"event"`
	Actor      string            `json:"actor"`
	Payload    string            `json:"payload"`
	Labels     map[string]string `json:"labels"`
	Selector   string            `json:"selector"`
	MateWanted int               `json:"mate_wanted"`
	Context    context.Context   `json:"-"`
}

type EventActorMessage struct {
	Params    Params          `json:"params"`
	RID       string          `json:"-"`
	OutChan   chan OutValue   `json:"-"`
	MatchChan chan string     `json:"-"`
	Selector  labels.Selector `json:"-"`
	Labels    labels.Set      `json:"-"`
	CreatedAt time.Time       `json:"created_at"`
}

type OutValue struct {
	Payloads map[string]string `json:"payloads"`
}

type EventToActorToRequestMap struct {
	Map map[string]map[string]map[string]EventActorMessage
	mu  *sync.RWMutex
}

type LogEvent struct {
	Time       time.Time  `json:"time"`
	RID        string     `json:"rid"`
	MatchID    string     `json:"match_id,omitempty"`
	Msg        string     `json:"msg"`
	Event      string     `json:"event"`
	Actor      string     `json:"actor"`
	Selector   string     `json:"selector"`
	Labels     labels.Set `json:"labels"`
	MateWanted int        `json:"mate_wanted"`
	Payload    string     `json:"payload"`
	Pending    int32      `json:"pending"`
	Age        float64    `json:"age"`
}

type Syncnet struct {
	eventRequestChan         chan EventActorMessage
	eventToActorToRequestMap EventToActorToRequestMap
}

func NewSyncnet() *Syncnet {
	sn := Syncnet{}

	var pendingCounter int32
	cleanChan := make(chan string)
	sn.eventRequestChan = make(chan EventActorMessage)
	sn.eventToActorToRequestMap = EventToActorToRequestMap{
		Map: make(map[string]map[string]map[string]EventActorMessage),
		mu:  &sync.RWMutex{},
	}

	go func() {
		for {
			completedRID := <-cleanChan
			cleanMap(sn.eventToActorToRequestMap, completedRID)
		}
	}()

	go func() {
		for eventReq := range sn.eventRequestChan {
			atomic.AddInt32(&pendingCounter, 1)
			fmt.Println(newLog(eventReq, "+", atomic.LoadInt32(&pendingCounter), ""))

			go func(eventReq EventActorMessage) {
				matchID := ""

				select {
				case matchID = <-eventReq.MatchChan:
				case <-eventReq.Params.Context.Done():
				}

				<-eventReq.Params.Context.Done()

				atomic.AddInt32(&pendingCounter, -1)
				fmt.Println(newLog(eventReq, "-", atomic.LoadInt32(&pendingCounter), matchID))
			}(eventReq)

			requests, ok := func(eventReq EventActorMessage) ([]EventActorMessage, bool) {
				requests := []EventActorMessage{eventReq}
				if eventReq.Params.MateWanted == 0 {
					return requests, true
				}

				maxMateCount := eventReq.Params.MateWanted
				sn.eventToActorToRequestMap.mu.RLock()
				defer sn.eventToActorToRequestMap.mu.RUnlock()

				if actorToRequestMap, ok := sn.eventToActorToRequestMap.Map[eventReq.Params.Event]; ok {
					for _, actorPendingReqs := range actorToRequestMap {

						for _, pendingReq := range actorPendingReqs {
							if !eventReq.Selector.Matches(pendingReq.Labels) || !pendingReq.Selector.Matches(eventReq.Labels) {
								continue
							}

							select {
							case <-pendingReq.Params.Context.Done():
								continue
							default:
								requests = append(requests, pendingReq)
								if maxMateCount < pendingReq.Params.MateWanted {
									maxMateCount = pendingReq.Params.MateWanted
								}
							}

							// -1 to exclude the current request
							if len(requests)-1 == maxMateCount {
								return requests, true
							}
						}
					}
				}
				return []EventActorMessage{}, false
			}(eventReq)

			if ok {
				rids := make([]string, 0, len(requests))
				outValue := OutValue{
					Payloads: map[string]string{},
				}

				for _, req := range requests {
					outValue.Payloads[req.Params.Actor] = req.Params.Payload
					rids = append(rids, req.RID)
				}
				for _, req := range requests {
					select {
					case req.MatchChan <- strings.Join(rids, "+"):
					case <-req.Params.Context.Done():
					}

					select {
					case req.OutChan <- outValue:
					case <-req.Params.Context.Done():
					}
				}

				continue
			}

			// if we are here, it means no other actor was
			// listening, or sn.eventToActorToRequestMap has not
			// cleaned up cancelled or done requests yet.

			func() {
				sn.eventToActorToRequestMap.mu.Lock()
				defer sn.eventToActorToRequestMap.mu.Unlock()

				if _, ok := sn.eventToActorToRequestMap.Map[eventReq.Params.Event]; !ok {
					sn.eventToActorToRequestMap.Map[eventReq.Params.Event] = map[string]map[string]EventActorMessage{}
				}
				if _, ok := sn.eventToActorToRequestMap.Map[eventReq.Params.Event][eventReq.Params.Actor]; !ok {
					sn.eventToActorToRequestMap.Map[eventReq.Params.Event][eventReq.Params.Actor] = map[string]EventActorMessage{}
				}
				sn.eventToActorToRequestMap.Map[eventReq.Params.Event][eventReq.Params.Actor][eventReq.RID] = eventReq

				go func(eventReq EventActorMessage) {
					<-eventReq.Params.Context.Done()
					cleanChan <- eventReq.RID
				}(eventReq)
			}()

		}
	}()

	return &sn
}

func (sn *Syncnet) Send(params Params) (chan OutValue, error) {

	if params.Event == "" {
		return nil, fmt.Errorf("empty event")
	}

	if params.Actor == "" {
		return nil, fmt.Errorf("empty actor")
	}

	if params.Selector == "" {
		params.Selector = fmt.Sprintf("actor != %s", params.Actor)
	}

	lq, err := labels.Parse(params.Selector)
	if err != nil {
		return nil, err
	}

	if params.MateWanted == 0 {
		params.MateWanted = 1
	}

	if params.Context == nil {
		params.Context = context.Background()
	}

	readyChan := make(chan OutValue)
	sn.eventRequestChan <- EventActorMessage{
		RID:       RandStringRunes(8),
		Params:    params,
		Selector:  lq,
		Labels:    labels.Set(params.Labels),
		OutChan:   readyChan,
		CreatedAt: time.Now(),
		MatchChan: make(chan string),
	}

	return readyChan, nil
}

func (sn *Syncnet) NewHandler() http.Handler {
	serveMux := http.NewServeMux()

	serveMux.HandleFunc("/stats", func(rw http.ResponseWriter, r *http.Request) {
		sn.eventToActorToRequestMap.mu.RLock()
		defer sn.eventToActorToRequestMap.mu.RUnlock()

		rw.Write(mustMarshalJson(sn.eventToActorToRequestMap.Map))
		rw.Write([]byte("\n"))
	})

	serveMux.HandleFunc("/event", func(rw http.ResponseWriter, r *http.Request) {
		rid := RandStringRunes(8)
		event := r.URL.Query().Get("event")
		actor := r.URL.Query().Get("actor")
		payload := r.URL.Query().Get("payload")

		mateCount := 1
		selector := fmt.Sprintf("actor != %s", actor)

		if r.URL.Query().Get("selector") != "" {
			selector = r.URL.Query().Get("selector")
		}

		lq, err := labels.Parse(selector)
		if err != nil {
			rw.Write([]byte(fmt.Sprintf("bad selector: %v\n", err)))
			return
		}

		if r.URL.Query().Get("mates") != "" {
			var err error
			mateCount, err = strconv.Atoi(r.URL.Query().Get("mates"))
			if err != nil {
				rw.Write([]byte("mates must be int\n"))
				return
			}
		}

		params := Params{
			Event:      event,
			Actor:      actor,
			Payload:    payload,
			Labels:     labels.Set{"actor": actor},
			Selector:   selector,
			MateWanted: mateCount,
			Context:    r.Context(),
		}

		readyChan := make(chan OutValue)
		sn.eventRequestChan <- EventActorMessage{
			RID:       rid,
			Params:    params,
			OutChan:   readyChan,
			CreatedAt: time.Now(),
			MatchChan: make(chan string),
			Selector:  lq,
			Labels:    labels.Set{"actor": actor},
		}

		select {
		case out := <-readyChan:
			rw.Write(mustMarshalJson(out))
			rw.Write([]byte("\n"))
		case <-r.Context().Done():
		}
	})
	return serveMux
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
						delete(eventToActorToRequestMap.Map, eventReq.Params.Event)
					}
					return
				}
			}
		}
	}

}

var letterRunes = []rune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ")

func newLog(eventReq EventActorMessage, msg string, pendingCounter int32, matchID string) string {
	return string(mustMarshalJson(LogEvent{
		RID:        eventReq.RID,
		Actor:      eventReq.Params.Actor,
		Selector:   eventReq.Selector.String(),
		Labels:     eventReq.Labels,
		Event:      eventReq.Params.Event,
		MateWanted: eventReq.Params.MateWanted,
		MatchID:    matchID,
		Pending:    pendingCounter,
		Msg:        msg,
		Time:       time.Now(),
		Payload:    eventReq.Params.Payload,
		Age:        time.Since(eventReq.CreatedAt).Seconds(),
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
