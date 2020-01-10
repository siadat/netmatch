package netsync

import (
	"bytes"
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

	"gopkg.in/yaml.v2"
	"k8s.io/apimachinery/pkg/labels"
)

type Params struct {
	// Event is the name of event. It is used to match events when syncing.
	Event string `json:"event" yaml:event`
	// Actor is a label that is used to filter events when matching. It is
	// basically a label used by the default selector.
	Actor string `json:"actor" yaml:"actor"`
	// Payload is a string that is broadcast in the response to all events
	// that are synced together.
	Payload string `json:"payload" yaml:"payload"`
	// Labels are used by selector to filter what events can match each
	// other.
	Labels map[string]string `json:"labels" yaml:"labels"`
	// Selector is used to filter events by their labels.
	// For example, event1 will match event2, if event2.Labels match
	// event1.Selector.
	Selector string `json:"selector" yaml:"selector"`
	// Mates is the number of other events that should be present in order
	// for this event to sync with them.
	Mates int `json:"mates" yaml:"mates"`
	// Context is used for cancelling an event.
	Context context.Context `json:"-" yaml:"-"`
}

type eventActorMsgStruct struct {
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

type eventToActorToReqStruct struct {
	Map map[string]map[string]map[string]eventActorMsgStruct
	mu  *sync.RWMutex
}

type logEventStruct struct {
	Time     time.Time  `json:"time,omitempty"`
	RID      string     `json:"rid"`
	MatchID  string     `json:"match_id,omitempty"`
	Msg      string     `json:"msg"`
	Event    string     `json:"event"`
	Actor    string     `json:"actor"`
	Selector string     `json:"selector,omitempty"`
	Labels   labels.Set `json:"labels,omitempty"`
	Mates    int        `json:"mates"`
	Payload  string     `json:"payload"`
	Pending  int32      `json:"pending"`
	Age      float64    `json:"age"`
}

type graphLineStruct struct {
	rid      string
	str      string
	occupied bool
}

type Netsync struct {
	LogFormat string

	terminateChan chan struct{}
	terminateWG   sync.WaitGroup

	graphline   []graphLineStruct
	graphlineMu *sync.Mutex

	pendingCounter           int32
	eventRequestChan         chan eventActorMsgStruct
	eventToActorToRequestMap eventToActorToReqStruct
}

// Close tears down internal goroutines to free up resources. It
// blocks until all internal goroutines are stopped, which should
// happen immediately.
func (ns *Netsync) Close() {
	close(ns.terminateChan)
	ns.terminateWG.Wait()
}

func NewNetsync() *Netsync {
	ns := Netsync{}

	cleanChan := make(chan string)

	ns.LogFormat = "graph"
	ns.graphlineMu = &sync.Mutex{}

	ns.terminateChan = make(chan struct{})
	ns.terminateWG = sync.WaitGroup{}
	ns.eventRequestChan = make(chan eventActorMsgStruct)
	ns.eventToActorToRequestMap = eventToActorToReqStruct{
		Map: make(map[string]map[string]map[string]eventActorMsgStruct),
		mu:  &sync.RWMutex{},
	}

	ns.terminateWG.Add(1)
	go func() {
		defer ns.terminateWG.Done()
		for {
			select {
			case <-ns.terminateChan:
				return
			case completedRID := <-cleanChan:
				cleanMap(ns.eventToActorToRequestMap, completedRID)
			}
		}
	}()

	ns.terminateWG.Add(1)
	go func() {
		defer ns.terminateWG.Done()
		for {
			var eventReq eventActorMsgStruct
			select {
			case <-ns.terminateChan:
				return
			case eventReq = <-ns.eventRequestChan:
			}

			atomic.AddInt32(&ns.pendingCounter, 1)
			fmt.Println(ns.newLog([]eventActorMsgStruct{eventReq}, "+", ""))
			ns.terminateWG.Add(1)
			go func(eventReq eventActorMsgStruct) {

				matchID := ""
				defer func() {
					atomic.AddInt32(&ns.pendingCounter, -1)
					if !(ns.LogFormat == "graph" && matchID != "") {
						fmt.Println(ns.newLog([]eventActorMsgStruct{eventReq}, "-", matchID))
					}
					ns.terminateWG.Done()
				}()

				select {
				case <-ns.terminateChan:
					return
				case matchID = <-eventReq.MatchChan:
				case <-eventReq.Params.Context.Done():
				}

				select {
				case <-ns.terminateChan:
					return
				case <-eventReq.Params.Context.Done():
					// checking context again, even though
					// we already had it in the previous
					// select{} statement, because we could
					// be here because MatchChan was ready,
					// and not because the request is done.
					// and we want to proceed only when the
					// request is done.
				}
			}(eventReq)

			requests, ok := func(eventReq eventActorMsgStruct) ([]eventActorMsgStruct, bool) {
				requests := []eventActorMsgStruct{eventReq}
				if eventReq.Params.Mates == 0 {
					return requests, true
				}

				maxMateCount := eventReq.Params.Mates
				ns.eventToActorToRequestMap.mu.RLock()
				defer ns.eventToActorToRequestMap.mu.RUnlock()

				if actorToRequestMap, ok := ns.eventToActorToRequestMap.Map[eventReq.Params.Event]; ok {
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
								if maxMateCount < pendingReq.Params.Mates {
									maxMateCount = pendingReq.Params.Mates
								}
							}

							// -1 to exclude the current request
							if len(requests)-1 == maxMateCount {
								return requests, true
							}
						}
					}
				}
				return []eventActorMsgStruct{}, false
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

				matchID := strings.Join(rids, "+")
				fmt.Println(ns.newLog(requests, "m", matchID))

				for _, req := range requests {
					select {
					case req.MatchChan <- matchID:
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
			// listening, or ns.eventToActorToRequestMap has not
			// cleaned up cancelled or done requests yet.

			func() {
				ns.eventToActorToRequestMap.mu.Lock()
				defer ns.eventToActorToRequestMap.mu.Unlock()

				if _, ok := ns.eventToActorToRequestMap.Map[eventReq.Params.Event]; !ok {
					ns.eventToActorToRequestMap.Map[eventReq.Params.Event] = map[string]map[string]eventActorMsgStruct{}
				}
				if _, ok := ns.eventToActorToRequestMap.Map[eventReq.Params.Event][eventReq.Params.Actor]; !ok {
					ns.eventToActorToRequestMap.Map[eventReq.Params.Event][eventReq.Params.Actor] = map[string]eventActorMsgStruct{}
				}
				ns.eventToActorToRequestMap.Map[eventReq.Params.Event][eventReq.Params.Actor][eventReq.RID] = eventReq

				ns.terminateWG.Add(1)
				go func(eventReq eventActorMsgStruct) {
					defer ns.terminateWG.Done()

					select {
					case <-ns.terminateChan:
						return
					case <-eventReq.Params.Context.Done():
						cleanChan <- eventReq.RID
					}
				}(eventReq)
			}()

		}
	}()

	return &ns
}

func (ns *Netsync) newLog(eventReqs []eventActorMsgStruct, msg string, matchID string) string {
	switch ns.LogFormat {
	case "json":
		for _, eventReq := range eventReqs {
			return string(mustMarshalJson(logEventStruct{
				RID:      eventReq.RID,
				Actor:    eventReq.Params.Actor,
				Selector: eventReq.Selector.String(),
				Labels:   eventReq.Labels,
				Event:    eventReq.Params.Event,
				Mates:    eventReq.Params.Mates,
				MatchID:  matchID,
				Pending:  atomic.LoadInt32(&ns.pendingCounter),
				Msg:      msg,
				Time:     time.Now(),
				Payload:  eventReq.Params.Payload,
				Age:      time.Since(eventReq.CreatedAt).Seconds(),
			}))
		}
	case "graph":
		ns.graphlineMu.Lock()
		defer ns.graphlineMu.Unlock()

		LINE_STR := "│"
		START_STR := "┌"
		END_STR := "└"
		CANCEL_STR := "┴"

		for i := range ns.graphline {
			if ns.graphline[i].occupied {
				ns.graphline[i].str = LINE_STR
			} else {
				ns.graphline[i].str = " "
			}
		}

		switch msg {
		case "+":
			for _, eventReq := range eventReqs {
				vacantFound := false
				for i := range ns.graphline {
					if !ns.graphline[i].occupied {
						ns.graphline[i].rid = eventReq.RID
						ns.graphline[i].str = START_STR
						ns.graphline[i].occupied = true
						vacantFound = true
						break
					}
				}
				if !vacantFound {
					ns.graphline = append(ns.graphline, graphLineStruct{
						rid:      eventReq.RID,
						str:      START_STR,
						occupied: true,
					})
				}
			}
		case "-", "m":
			var chr string
			if msg == "-" {
				chr = CANCEL_STR
			} else {
				chr = END_STR
			}
			for _, eventReq := range eventReqs {
				for i := range ns.graphline {
					if eventReq.RID == ns.graphline[i].rid {
						ns.graphline[i].rid = eventReq.RID
						ns.graphline[i].str = chr
						ns.graphline[i].occupied = false
						break
					}
				}
			}
		}

		var buf *bytes.Buffer

		if false {
			buf = bytes.NewBufferString(time.Now().Format(time.RFC3339) + " ")
		} else {
			buf = bytes.NewBufferString("")
		}

		for _, gl := range ns.graphline {
			buf.WriteString(fmt.Sprintf("%s", gl.str))
		}

		if msg == "m" {
			buf.WriteString(fmt.Sprintf(" match"))
		} else if msg == "-" {
			buf.WriteString(fmt.Sprintf(" stopped"))
		}

		for _, eventReq := range eventReqs {
			buf.WriteString(fmt.Sprintf(" [e=%s a=%s]", eventReq.Params.Event, eventReq.Params.Actor))
		}

		for {
			if len(ns.graphline) == 0 {
				break
			}

			if ns.graphline[len(ns.graphline)-1].occupied == false {
				// if it is the last item, shrink the slice
				ns.graphline = ns.graphline[:len(ns.graphline)-1]
			} else {
				break
			}
		}

		return buf.String()
	}

	return fmt.Sprintf("unknown format %q", ns.LogFormat)
}

// Send will dispatch an event with params.
// This function will not block.
// The returned channel should be used to await synchronization of this event.
//
//     doneChan, err := ns.Send(netsync.Params{
//       Actor: "CUST",
//       Event: "choc",
//       Payload: "Please give me a chocolate",
//     })
//
//     output := <-doneChan // this will block until sync
func (ns *Netsync) Send(params Params) (chan OutValue, error) {

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

	if params.Mates == 0 {
		params.Mates = 1
	}

	if params.Context == nil {
		params.Context = context.Background()
	}

	readyChan := make(chan OutValue)
	ns.eventRequestChan <- eventActorMsgStruct{
		RID:       randStringRunes(8),
		Params:    params,
		Selector:  lq,
		Labels:    labels.Set(params.Labels),
		OutChan:   readyChan,
		CreatedAt: time.Now(),
		MatchChan: make(chan string),
	}

	return readyChan, nil
}

func (ns *Netsync) NewHandler() http.Handler {
	serveMux := http.NewServeMux()

	serveMux.HandleFunc("/stats", func(rw http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		ns.eventToActorToRequestMap.mu.RLock()
		defer ns.eventToActorToRequestMap.mu.RUnlock()

		rw.Write(mustMarshalJson(ns.eventToActorToRequestMap.Map))
		rw.Write([]byte("\n"))
	})

	serveMux.HandleFunc("/event", func(rw http.ResponseWriter, r *http.Request) {
		inputFormat := r.URL.Query().Get("input")
		if inputFormat == "" {
			inputFormat = "url"
		}

		params := Params{
			Mates: 1, // default
		}

		var err error
		var lq labels.Selector
		labelsMap := map[string]string{}

		switch inputFormat {
		case "json", "yaml":
			switch inputFormat {
			case "yaml":
				decoder := yaml.NewDecoder(r.Body)
				decoder.SetStrict(true)
				err := decoder.Decode(&params)
				if err != nil {
					rw.Write([]byte(fmt.Sprintf("failed to unmarshal body: %v\n", err)))
					return
				}
				defer r.Body.Close()
			case "json":
				decoder := json.NewDecoder(r.Body)
				err := decoder.Decode(&params)
				if err != nil {
					rw.Write([]byte(fmt.Sprintf("failed to unmarshal body: %v\n", err)))
					return
				}
				defer r.Body.Close()
			}

			selectorStr := params.Selector
			params.Context = r.Context()

			if len(labelsMap) == 0 {
				labelsMap = labels.Set{"actor": params.Actor}
			}

			if len(selectorStr) == 0 {
				selectorStr = fmt.Sprintf("actor != %s", params.Actor)
			}

			lq, err = labels.Parse(selectorStr)
			if err != nil {
				rw.Write([]byte(fmt.Sprintf("failed to parse selector %q: %v\n", selectorStr, err)))
				return
			}
		case "url": // To be deprecated
			r.Body.Close()
			event := r.URL.Query().Get("event")
			actor := r.URL.Query().Get("actor")
			payload := r.URL.Query().Get("payload")

			labelsStr := r.URL.Query().Get("labels")
			selectorStr := r.URL.Query().Get("selector")

			if len(labelsStr) == 0 {
				labelsMap = labels.Set{"actor": actor}
			} else {
				for _, l := range strings.Split(labelsStr, ",") {
					keyval := strings.Split(l, "=")
					if len(keyval) != 2 {
						rw.Write([]byte(fmt.Sprintf("bad label: %q\n", keyval)))
						return
					}
					key := keyval[0]
					val := keyval[1]
					labelsMap[key] = val
				}
			}

			mateCount := 1

			if selectorStr == "" {
				selectorStr = fmt.Sprintf("actor != %s", actor)
			}

			lq, err = labels.Parse(selectorStr)
			if err != nil {
				rw.Write([]byte(fmt.Sprintf("failed to parse selector %q: %v\n", selectorStr, err)))
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

			params = Params{
				Event:    event,
				Actor:    actor,
				Payload:  payload,
				Labels:   labelsMap,
				Selector: selectorStr,
				Mates:    mateCount,
				Context:  r.Context(),
			}
		}

		readyChan := make(chan OutValue)
		ns.eventRequestChan <- eventActorMsgStruct{
			RID:       randStringRunes(8),
			Params:    params,
			OutChan:   readyChan,
			CreatedAt: time.Now(),
			MatchChan: make(chan string),
			Selector:  lq,
			Labels:    labels.Set(labelsMap),
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

func cleanMap(eventToActorToRequestMap eventToActorToReqStruct, toBeCleanedRID string) {
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

func randStringRunes(n int) string {
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
