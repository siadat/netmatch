# Netmatch
<a href="https://i.imgur.com/RcdPVwR.png"><img style="object-fit: cover; height: 100px; width:100%" src="https://i.imgur.com/RcdPVwR.png"/></a>

[![GoDoc](https://godoc.org/github.com/siadat/netmatch?status.svg)][godoc]
[![Build Status](https://travis-ci.org/siadat/netmatch.svg?branch=master)][travis]
[![Go Report Card](https://goreportcard.com/badge/github.com/siadat/netmatch)][goreportcard]

[godoc]:  https://godoc.org/github.com/siadat/netmatch
[travis]: https://travis-ci.org/siadat/netmatch
[goreportcard]: https://goreportcard.com/report/github.com/siadat/netmatch
[csp_homepage]: http://www.usingcsp.com/
[k8s_labels_and_selectors]: https://kubernetes.io/docs/concepts/overview/working-with-objects/labels/

- **What is Netmatch?**
  It is a tool for *matching* and *synchronizing* HTTP requests.
- **What is synchronizing?**
  Synchronizing two requests means that the first one is blocked until the second one arrives as well.
- **What is matching?**
  Only matching requests are synchronized. Matching two requests means the selectors of one matches the labels of the other.
- **Give me an example.**
  One process sends an HTTP request, and it is blocked until N
  other matching processes send N other requests with the same key.
  The requests might have a payload value, which is shared with all requests in
  the responses they eventually receive upon matching.
  Matching of requests can be limited using labels and selectors.
- **What is it used for?**
  It could be used for matching online players in a multiplayer game server.
  In general, it could be used in a transaction when it is requires that several
  processes to do something or nothing at all.

## Quick start (sync two processes)

Start the server:

```bash
go install github.com/siadat/netmatch/cmd/netmatch
netmatch :8000
```

In terminal 1:

```bash
echo '{key: e, payload: p1, selector: "name != actor1", labels: {name: actor1}}' | curl -d@- localhost:8000/match?input=yaml
```

In terminal 2:

```bash
echo '{key: e, payload: p2, selector: "name != actor2", labels: {name: actor2}}' | curl -d@- localhost:8000/match?input=yaml
```

Both of these will receive the following JSON response:

```json
{
 "requests": [
  {
   "labels": { "name": "actor2" },
   "payload": "p2"
  },
  {
   "labels": { "name": "actor1" },
   "payload": "p1"
  }
 ]
}
```

**Note 1**: These two requests matched and synced together because the following conditions are met:

- **Identical keys**: the keys are the same, i.e., `e`, and;
- **Selector 1 matches label 2**: 1st request's selector (`name != actor1`) matches 2nd request's label (`name = actor2`), and;
- **Selector 2 matches label 1**: 2nd request's selector (`name != actor2`) matches 1st request's label (`name = actor1`), and;
- **Enough matching requests**: each request is asking for 1 other request to match (because the default value of `count` is 1) and there are already 2 requests.

**Note 2**: Because we set `input=yaml` parameters are parsed from request body. Alternatively, we could use JSON request body, or URL queries only. Using URL queries, we would rewrite the last 2 requests:

```bash
curl 'http://localhost:8000/match?key=e&payload=p1&selector=name!%3Dn1&labels=name%3Dn1' &
curl 'http://localhost:8000/match?key=e&payload=p2&selector=name!%3Dn2&labels=name%3Dn2' &
```

## Example (match two players)

```bash
echo '{key: newGame, selector: "id != player1", labels: {id: player1}}' | curl -d@- 0:8000/match?input=yaml &
echo '{key: newGame, selector: "id != player2", labels: {id: player2}}' | curl -d@- 0:8000/match?input=yaml &
```

## Example (match two players and one game maker)

The game maker process creates a game with gameid=123 as its payload, and sends a request for 2 matching requests (`count: 2`):

```bash
echo '{key: joinGame, count: 2, payload: "gameid is 123", selector: "id != gameMaker", labels: {id: gameMaker}}' | curl -d@- 0:8000/match?input=yaml &
```

You can inspect this pending request by calling the stats endpoint:

```bash
curl http://0:8000/stats
{
 "joinGame": {
  "MaPEZQle": {
   "params": {
    "key": "joinGame",
    "payload": "gameid is 123",
    "labels": {
     "id": "gameMaker"
    },
    "selector": "id != gameMaker",
    "count": 2
   },
   "created_at": "2020-01-10T21:31:47.346538739+03:30"
  }
 }
}
```

Finally, lets add the two players:

```bash
echo '{key: joinGame, count: 2, selector: "id != player1", labels: {id: player1}}' | curl -d@- 0:8000/match?input=yaml &
echo '{key: joinGame, count: 2, selector: "id != player2", labels: {id: player2}}' | curl -d@- 0:8000/match?input=yaml &
```

All three processes (players and the game maker) will receive this response:

```json
{
 "requests": [
  {
   "labels": { "id": "player2" },
   "payload": ""
  },
  {
   "labels": { "id": "player1" },
   "payload": ""
  },
  {
   "labels": { "id": "gameMaker" },
   "payload": "gameid is 123"
  }
 ]
}
```

## I still don't get it...

Suppose we have two programs running concurrently,
and these processes are required to synchronize on EVENT before proceeding.
The logs of these processes could look like this (without synchronization):

    time  Process 1  Process 2
    ----  ---------  ---------
    1     p1.log1    p2.log1
    2                p2.log2
    3     p1.log2
    4     p1.EVENT   p2.log3
    5     p1.log3    p2.log4
    6                p2.EVENT
    7                p2.log5

We want this EVENT to be synchronized across the two processes, so, the desired log should look like (with synchronization):

    time  Process 1  Process 2
    ----  ---------  ---------
    1     p1.log1    p2.log1
    2                p2.log2
    3     p1.log2
    4   ┌>p1.EVENT   p2.log3
    5   │            p2.log4
    6   └───────────>p2.EVENT
    7     p1.log3    p2.log5

Notice that p1.log3 moved further down in the timeline, from time=5 to time=7.
When p1.EVENT happens, Process 1 is blocked for 2 time-units and p1.log3 is not executed until p2.EVENT is executed as well.

To achieve this in Syncnet, Process 1 should send a match request with key=EVENT before p1.log3,
and Process 2 should send a match request with key=EVENT before p2.log5

Feel free to open an issue if you think you don't understand it yet, or send a PR if you have good explanations and examples.

## Concepts

- Key: A key is used to identify which requests can be matched with each other.
- Count: The number of other requests that are expected to be present for match to happen. A match could require 1, 2, or more matching requests.
- Selector: A selector is used in a request to specify the desired requests it wants to match with.
- Label: Each request has one or several labels that other requests use with their selectors to see if they are interested in a match.

## Endpoints

There are 2 endpoints:

### /match

This is the main endpoint. Clients send requests for matching with other requests using this endpoint.

### /stats

This endpoint is used to monitor the current pending/blocking requests waiting for matching requests.

## Params

### input
This is a URL query parameter that specifies the format in which params are provided.

- `&input=url` (default): look for params in the URL queries.
- `&input=json`: look for a JSON object in the request body.
- `&input=yaml`: look for a YAML object in the request body.

### key
**Required** Requests with identical keys are matched. See labels and
selectors for more fine-grained control over matching.

### payload
The data that are shared with every matched requests when a match is made.

### count
The value of `count` indicates the number of other requests
that are required to be present for a request to be matched. A value of 0 means no
matching is necessary, 1 means a pair of requests are required to be present, 3
and more means 4 or more requests are required to be present.

The default value is 1.

### selector
Selectors filter what requests do or do not match with a request. E.g., a request might only want to match with the requests with or without a particular label.
The formatting is identical to that of the [Labels and Selectors][k8s_labels_and_selectors] of Kubernetes.

### labels
Labels are used by selectors (see above).
When input=json and input=yaml, labels are given using a key-value map.
When input=url, they are a comma separated list of `key1=value1,key2=value2` items.

## Using the Go API

```go
nm := netmatch.NewNetmatch()
defer nm.Close()

ctx, cancel := context.WithCancel(context.Background)
defer cancel()

readyChan, err := nm.Match(netmatch.Params{
  Key: "joinGame",
  Count: 1,
  Labels: map[string]string{
    "id":   "1",
    "kind": "player",
  },
  Selector: "kind == player && id != 1",
  Payload: "I want to join a game with another player",
  Context: ctx,
})

if err != nil {
  panic(err)
}

select {
  case output := <-readyChan: // match made!
  case <-ctx.Done():          // cancelled
}
```

## Comparison with Go channels

Syncnet behaves similar to a Go unbuffered channel, however, there are differences:

- In Go, only 2 goroutines an unbuffered channel syncs only 2 goroutines.
  With Syncnet, we could synchronize any number of requests.
  Also, each request could ask for a different number of matching requests, by setting the `count` param.
- Go channels provide no filtering of the messages for receiving goroutines.
  Syncnet filters what requests match your request using labels and selectors.
- Netmatch is a service that can be used to handle requests coming from processes running on different servers.
  Netmatch also provides a Go API ([Go docs][godoc]) which could be used to synchronize goroutines.

## Background

This tool is inspired by the ideas in [CSP][csp_homepage] and Go unbuffered channels.
Issues and PRs are welcome. :)
