package netmatch

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

// Params is a collection of parameters that can be set to configure a request
// for sync.
type Params struct {
	// Key is used to match requests when matching. All matching requests must
	// have identical keys.
	Key string `json:"key" yaml:"key"`
	// Actor is a label that is used to filter requests when matching. It is
	// basically a label used by the default selector.
	Actor string `json:"actor" yaml:"actor"`
	// Payload is a string that is broadcast in the response to all
	// requests that are matched together.
	Payload string `json:"payload" yaml:"payload"`
	// Labels are used by selector to filter what requests match each
	// other.
	Labels map[string]string `json:"labels" yaml:"labels"`
	// Selector is used to filter keys by their labels.
	// For example, request1 will match request2, if request2.Labels match
	// request1.Selector.
	Selector string `json:"selector" yaml:"selector"`
	// Count is the number of other requests that should be present in order
	// for this request to match with them.
	Count int `json:"count" yaml:"count"`
	// Context is used by the client to let Syncnet know that the current
	// request is cancelled and is no longer looking for a match.
	Context context.Context `json:"-" yaml:"-"`
}

type reqStruct struct {
	Params    Params          `json:"params"`
	RID       string          `json:"-"`
	OutChan   chan OutValue   `json:"-"`
	MatchChan chan string     `json:"-"`
	Selector  labels.Selector `json:"-"`
	Labels    labels.Set      `json:"-"`
	CreatedAt time.Time       `json:"created_at"`
}

// OutValue is the struct that is returned when a match is made.
// The main HTTP handler uses this struct to create a JSON for the responses.
type OutValue struct {
	Payloads map[string]string `json:"payloads"`
}

type keyToActorToReqStruct struct {
	Map map[string]map[string]map[string]reqStruct
	mu  *sync.RWMutex
}

type logStruct struct {
	Time     time.Time  `json:"time,omitempty"`
	RID      string     `json:"rid"`
	MatchID  string     `json:"match_id,omitempty"`
	Msg      string     `json:"msg"`
	Key      string     `json:"key"`
	Actor    string     `json:"actor"`
	Selector string     `json:"selector,omitempty"`
	Labels   labels.Set `json:"labels,omitempty"`
	Count    int        `json:"count"`
	Payload  string     `json:"payload"`
	Pending  int32      `json:"pending"`
	Age      float64    `json:"age"`
}

type graphLineStruct struct {
	rid      string
	str      string
	occupied bool
}

// Netmatch is the main struct containing everything needed to run a netmatch
// server.
type Netmatch struct {
	// LogFormat is the format of logs. Possible values are "json" and "graph" (default).
	LogFormat string

	terminateChan chan struct{}
	terminateWG   sync.WaitGroup

	graphline   []graphLineStruct
	graphlineMu *sync.Mutex

	pendingCounter     int32
	matchReqChan       chan reqStruct
	keyToActorToReqMap keyToActorToReqStruct
}

// Close tears down internal goroutines to free up resources. It
// blocks until all internal goroutines are stopped, which should
// happen immediately.
func (nm *Netmatch) Close() {
	close(nm.terminateChan)
	nm.terminateWG.Wait()
}

// NewNetmatch initializes the server and returns a Netmatch struct.
func NewNetmatch() *Netmatch {
	nm := Netmatch{}

	cleanChan := make(chan string)

	nm.LogFormat = "graph"
	nm.graphlineMu = &sync.Mutex{}

	nm.terminateChan = make(chan struct{})
	nm.terminateWG = sync.WaitGroup{}
	nm.matchReqChan = make(chan reqStruct)
	nm.keyToActorToReqMap = keyToActorToReqStruct{
		Map: make(map[string]map[string]map[string]reqStruct),
		mu:  &sync.RWMutex{},
	}

	nm.terminateWG.Add(1)
	go func() {
		defer nm.terminateWG.Done()
		for {
			select {
			case <-nm.terminateChan:
				return
			case completedRID := <-cleanChan:
				cleanMap(nm.keyToActorToReqMap, completedRID)
			}
		}
	}()

	nm.terminateWG.Add(1)
	go func() {
		defer nm.terminateWG.Done()
		for {
			var matchReq reqStruct
			select {
			case <-nm.terminateChan:
				return
			case matchReq = <-nm.matchReqChan:
			}

			atomic.AddInt32(&nm.pendingCounter, 1)
			fmt.Println(nm.newLog([]reqStruct{matchReq}, "+", ""))
			nm.terminateWG.Add(1)
			go func(matchReq reqStruct) {

				matchID := ""
				defer func() {
					atomic.AddInt32(&nm.pendingCounter, -1)
					if !(nm.LogFormat == "graph" && matchID != "") {
						fmt.Println(nm.newLog([]reqStruct{matchReq}, "-", matchID))
					}
					nm.terminateWG.Done()
				}()

				select {
				case <-nm.terminateChan:
					return
				case matchID = <-matchReq.MatchChan:
				case <-matchReq.Params.Context.Done():
				}

				select {
				case <-nm.terminateChan:
					return
				case <-matchReq.Params.Context.Done():
					// checking context again, even though
					// we already had it in the previous
					// select{} statement, because we could
					// be here because MatchChan was ready,
					// and not because the request is done.
					// and we want to proceed only when the
					// request is done.
				}
			}(matchReq)

			requests, ok := func(matchReq reqStruct) ([]reqStruct, bool) {
				requests := []reqStruct{matchReq}
				if matchReq.Params.Count == 0 {
					return requests, true
				}

				maxCount := matchReq.Params.Count
				nm.keyToActorToReqMap.mu.RLock()
				defer nm.keyToActorToReqMap.mu.RUnlock()

				if actorToRequestMap, ok := nm.keyToActorToReqMap.Map[matchReq.Params.Key]; ok {
					for _, actorPendingReqs := range actorToRequestMap {

						for _, pendingReq := range actorPendingReqs {
							if !matchReq.Selector.Matches(pendingReq.Labels) || !pendingReq.Selector.Matches(matchReq.Labels) {
								continue
							}

							select {
							case <-pendingReq.Params.Context.Done():
								continue
							default:
								requests = append(requests, pendingReq)
								if maxCount < pendingReq.Params.Count {
									maxCount = pendingReq.Params.Count
								}
							}

							// -1 to exclude the current request
							if len(requests)-1 == maxCount {
								return requests, true
							}
						}
					}
				}
				return []reqStruct{}, false
			}(matchReq)

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
				fmt.Println(nm.newLog(requests, "m", matchID))

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
			// listening, or nm.keyToActorToReqMap has not
			// cleaned up cancelled or done requests yet.

			func() {
				nm.keyToActorToReqMap.mu.Lock()
				defer nm.keyToActorToReqMap.mu.Unlock()

				if _, ok := nm.keyToActorToReqMap.Map[matchReq.Params.Key]; !ok {
					nm.keyToActorToReqMap.Map[matchReq.Params.Key] = map[string]map[string]reqStruct{}
				}
				if _, ok := nm.keyToActorToReqMap.Map[matchReq.Params.Key][matchReq.Params.Actor]; !ok {
					nm.keyToActorToReqMap.Map[matchReq.Params.Key][matchReq.Params.Actor] = map[string]reqStruct{}
				}
				nm.keyToActorToReqMap.Map[matchReq.Params.Key][matchReq.Params.Actor][matchReq.RID] = matchReq

				nm.terminateWG.Add(1)
				go func(matchReq reqStruct) {
					defer nm.terminateWG.Done()

					select {
					case <-nm.terminateChan:
						return
					case <-matchReq.Params.Context.Done():
						cleanChan <- matchReq.RID
					}
				}(matchReq)
			}()

		}
	}()

	return &nm
}

func (nm *Netmatch) newLog(matchReqs []reqStruct, msg string, matchID string) string {
	switch nm.LogFormat {
	case "json":
		for _, matchReq := range matchReqs {
			return string(mustMarshalJSON(logStruct{
				RID:      matchReq.RID,
				Actor:    matchReq.Params.Actor,
				Selector: matchReq.Selector.String(),
				Labels:   matchReq.Labels,
				Key:      matchReq.Params.Key,
				Count:    matchReq.Params.Count,
				MatchID:  matchID,
				Pending:  atomic.LoadInt32(&nm.pendingCounter),
				Msg:      msg,
				Time:     time.Now(),
				Payload:  matchReq.Params.Payload,
				Age:      time.Since(matchReq.CreatedAt).Seconds(),
			}))
		}
	case "graph":
		nm.graphlineMu.Lock()
		defer nm.graphlineMu.Unlock()

		StrLine := "│"
		StrStart := "┌"
		StdEnd := "└"
		StrCancel := "┴"

		for i := range nm.graphline {
			if nm.graphline[i].occupied {
				nm.graphline[i].str = StrLine
			} else {
				nm.graphline[i].str = " "
			}
		}

		switch msg {
		case "+":
			for _, matchReq := range matchReqs {
				vacantFound := false
				for i := range nm.graphline {
					if !nm.graphline[i].occupied {
						nm.graphline[i].rid = matchReq.RID
						nm.graphline[i].str = StrStart
						nm.graphline[i].occupied = true
						vacantFound = true
						break
					}
				}
				if !vacantFound {
					nm.graphline = append(nm.graphline, graphLineStruct{
						rid:      matchReq.RID,
						str:      StrStart,
						occupied: true,
					})
				}
			}
		case "-", "m":
			var chr string
			if msg == "-" {
				chr = StrCancel
			} else {
				chr = StdEnd
			}
			for _, matchReq := range matchReqs {
				for i := range nm.graphline {
					if matchReq.RID == nm.graphline[i].rid {
						nm.graphline[i].rid = matchReq.RID
						nm.graphline[i].str = chr
						nm.graphline[i].occupied = false
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

		for _, gl := range nm.graphline {
			buf.WriteString(fmt.Sprintf("%s", gl.str))
		}

		if msg == "m" {
			buf.WriteString(fmt.Sprintf(" match"))
		} else if msg == "-" {
			buf.WriteString(fmt.Sprintf(" stopped"))
		}

		for _, matchReq := range matchReqs {
			buf.WriteString(fmt.Sprintf(" [e=%s a=%s]", matchReq.Params.Key, matchReq.Params.Actor))
		}

		for {
			if len(nm.graphline) == 0 {
				break
			}

			if nm.graphline[len(nm.graphline)-1].occupied == false {
				// if it is the last item, shrink the slice
				nm.graphline = nm.graphline[:len(nm.graphline)-1]
			} else {
				break
			}
		}

		return buf.String()
	}

	return fmt.Sprintf("unknown format %q", nm.LogFormat)
}

// Match will dispatch a request with the given params.
// This function will not block. The returned channel should be used to await
// matching of this request.
//
//     doneChan, err := nm.Match(netmatch.Params{
//       Actor: "CUST",
//       Key: "choc",
//       Payload: "Please give me a chocolate",
//     })
//
//     output := <-doneChan // this will block until match is made
func (nm *Netmatch) Match(params Params) (chan OutValue, error) {

	if params.Key == "" {
		return nil, fmt.Errorf("empty key")
	}

	if params.Actor == "" {
		return nil, fmt.Errorf("empty actor")
	}

	if params.Selector == "" {
		params.Selector = fmt.Sprintf("actor != %s", params.Actor)
	}
	if params.Labels == nil {
		params.Labels = make(map[string]string)
	}
	if len(params.Labels) == 0 {
		params.Labels["actor"] = params.Actor
	}

	lq, err := labels.Parse(params.Selector)
	if err != nil {
		return nil, err
	}

	// if params.Count == 0 { params.Count = 1 }

	if params.Context == nil {
		params.Context = context.Background()
	}

	// fmt.Printf("params = %+v\n", params)
	readyChan := make(chan OutValue)
	nm.matchReqChan <- reqStruct{
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

// NewHandler is used for creating an HTTP handler for the Netmatch server.
func (nm *Netmatch) NewHandler() http.Handler {
	serveMux := http.NewServeMux()

	serveMux.HandleFunc("/stats", func(rw http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		nm.keyToActorToReqMap.mu.RLock()
		defer nm.keyToActorToReqMap.mu.RUnlock()

		rw.Write(mustMarshalJSON(nm.keyToActorToReqMap.Map))
		rw.Write([]byte("\n"))
	})

	serveMux.HandleFunc("/match", func(rw http.ResponseWriter, r *http.Request) {
		inputFormat := r.URL.Query().Get("input")
		if inputFormat == "" {
			inputFormat = "url"
		}

		params := Params{
			Count: 1, // default
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
			key := r.URL.Query().Get("key")
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

			count := 1

			if selectorStr == "" {
				selectorStr = fmt.Sprintf("actor != %s", actor)
			}

			lq, err = labels.Parse(selectorStr)
			if err != nil {
				rw.Write([]byte(fmt.Sprintf("failed to parse selector %q: %v\n", selectorStr, err)))
				return
			}

			if r.URL.Query().Get("count") != "" {
				var err error
				count, err = strconv.Atoi(r.URL.Query().Get("count"))
				if err != nil {
					rw.Write([]byte("count must be int\n"))
					return
				}
			}

			params = Params{
				Key:      key,
				Actor:    actor,
				Payload:  payload,
				Labels:   labelsMap,
				Selector: selectorStr,
				Count:    count,
				Context:  r.Context(),
			}
		}

		readyChan := make(chan OutValue)
		nm.matchReqChan <- reqStruct{
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
			rw.Write(mustMarshalJSON(out))
			rw.Write([]byte("\n"))
		case <-r.Context().Done():
		}
	})

	return serveMux
}

func cleanMap(keyToActorToReqMap keyToActorToReqStruct, toBeCleanedRID string) {
	keyToActorToReqMap.mu.Lock()
	defer keyToActorToReqMap.mu.Unlock()

	for _, actorToRequestMap := range keyToActorToReqMap.Map {
		for actorName, pendingRequests := range actorToRequestMap {
			for rid, matchReq := range pendingRequests {
				if rid == toBeCleanedRID {
					delete(pendingRequests, rid)
					if len(pendingRequests) == 0 {
						delete(actorToRequestMap, actorName)
					}
					if len(actorToRequestMap) == 0 {
						delete(keyToActorToReqMap.Map, matchReq.Params.Key)
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

func mustMarshalJSON(v interface{}) []byte {
	byts, err := json.MarshalIndent(v, "", " ")
	check(err)
	return byts
}
