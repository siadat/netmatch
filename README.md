# Netsync

[![GoDoc](https://godoc.org/github.com/siadat/netsync?status.svg)][godoc]
[![Build Status](https://travis-ci.org/siadat/netsync.svg?branch=master)][travis]
[![Go Report Card](https://goreportcard.com/badge/github.com/siadat/netsync)][goreportcard]

[godoc]:  https://godoc.org/github.com/siadat/netsync
[travis]: https://travis-ci.org/siadat/netsync
[goreportcard]: https://goreportcard.com/report/github.com/siadat/netsync
[csp_homepage]: http://www.usingcsp.com/
[k8s_labels_and_selectors]: https://kubernetes.io/docs/concepts/overview/working-with-objects/labels/

Netsync is a synchronized messaging system.
It is a tool for synchronizing processes over the network via HTTP requests.
Synchronizing two requests means that the first one is blocked until the second one arrives as well.
Then both requests will receive a response with a payload.

It may be used for matching online players in a multiplayer game server.

## Comparison with Go channels

Syncnet behaves similar to a Go unbuffered channel, however, there are differences:

- In Go, only 2 goroutines an unbuffered channel syncs only 2 goroutines.
  With Syncnet, we could synchronize any number of requests.
  Also, each request could ask for a different number of matching requests, by setting the `mates` param.
- Go channels provide no filtering of the messages for receiving goroutines.
  Syncnet filters what requests match your request using labels and selectors.
- Netsync is a service that can be used to handle requests coming from processes running on different servers.
  Netsync also provides a Go API ([Go docs][godoc]) which could be used to synchronize goroutines.

## What does it do?

One process sends an HTTP request, and it is blocked until N
other matching processes send N other requests with the same "event" query value.
The requests might have a payload value, which is shared with all requests in
the responses they eventually receive upon syncing.
Matching of requests can be limited using labels and selectors.

## Quick start: sync 2 processes

Start the server:

```bash
go install github.com/siadat/netsync/cmd/netsync
netsync :8000
```

In terminal 1:

```bash
echo '{event: e, payload: p1, actor: actor1}' | curl -d@- localhost:8000/event?input=yaml
```

In terminal 2:

```bash
echo '{event: e, payload: p2, actor: actor2}' | curl -d@- localhost:8000/event?input=yaml
```

Both of these will receive the following JSON response:
```json
{
  "payloads": {
    "actor2": "p2",
    "actor1": "p1"
  }
}
```

**Note 1**: These two requests matched and synced together because the following conditions are met:

- **Identical event names**: the event names are the same, i.e., `e`, and;
- **Selector 1 matches label 2**: 1st request's selector (default: `actor != actor1`) matches 2nd request's label (default: `actor = actor2`), and;
- **Selector 2 matches label 1**: 2nd request's selector (default: `actor != actor2`) matches 1st request's label (default: `actor = actor1`), and;
- **Enough matching requests**: each request is asking for 1 other request to sync (because the default value of `mates` is 1) and there are already 2 requests.

**Note 2**: Because we set `input=yaml` parameters are parsed from request body. Alternatively, we could use JSON request body, or URL queries only. Using URL queries, we would rewrite the last 2 requests:

```bash
curl "http://localhost:8000/event?event=e&payload=p1&actor=actor1" &
curl "http://localhost:8000/event?event=e&payload=p2&actor=actor2" &
```

## Example: match 2 players

```bash
echo '{event: newGame, payload: p1, actor: player1}' | curl -d@- 0:8000/event?input=yaml &
echo '{event: newGame, payload: p2, actor: player2}' | curl -d@- 0:8000/event?input=yaml &
```

## Example: match 2 players and 1 game maker

The game maker process creates a game with gameid=123 as its payload, and sends a request for 2 mates:

```bash
echo '{event: joinGame, payload: "gameid=123", actor: gameMaker, mates: 2}' | curl -d@- 0:8000/event?input=yaml &
```

You can inspect this pending request by calling the stats endpoint:

```bash
curl http://0:8000/stats
{
  "joinGame": {
    "gameMaker": {
      "XVlBzgba": {
        "params": {
          "event": "joinGame",
          "actor": "gameMaker",
          "payload": "gameid=12345",
          "labels": {
            "actor": "gameMaker"
          },
          "selector": "actor != gameMaker",
          "mates": 2
        },
        "created_at": "2020-01-09T20:00:44.67685952+03:30"
      }
    }
  }
}
```

Finally, lets add the two players:

```bash
echo '{event: joinGame, payload: p1, actor: player1, mates: 2}' | curl -d@- 0:8000/event?input=yaml &
echo '{event: joinGame, payload: p2, actor: player2, mates: 2}' | curl -d@- 0:8000/event?input=yaml &
```

All three processes (players and the game maker) will receive this response:

```json
{
  "payloads": {
    "gameMaker": "gameid=12345",
    "player1": "p1",
    "player2": "p2"
  }
}
```

## I still don't get it...

Suppose we have two programs running concurrently,
and these processes are required to synchronize on an event EVENT before proceeding.
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

Perhaps Process 1 needs to read a file that is created by Process 2, so Process 1 must wait until that file is ready.
In this case, EVENT represents "file is ready".
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
When p1.Event happens, Process 1 is blocked for 2 time-units and p1.log3 is not executed until p2.EVENT is executed as well.

## Concepts

- Event: An event is used to identify which requests can be matched with each other.
- Actor: An actor is a client's name. It is used as a label to filter who can match who. ACtor option might get deprecated in favor of labels.
- Mate: A mate is the other side of the match. A match could require 1, 2, or more mates.
- Selector: A selector is used in a request to specify the desired requests it wants to match with.
- Label: Each request has one or several labels that other requests use with their selectors to see if they are interested in a match.

## Endpoints

There are 2 endpoints:

### /event

This is the main endpoint. Clients send requests for synchronizing events using this endpoint.

### /stats

This endpoint is used to monitor the current pending/blocking requests waiting for mates to sync.

## Params

### input
This is a URL query parameter that specifies the format in which params are provided.

- `&input=url` (default): look for params in the URL queries.
- `&input=json`: look for a JSON object in the request body.
- `&input=yaml`: look for a YAML object in the request body.

### event
**Required** The event key. Identical events are synchronized. See labels and
selectors for more fine-grained control over synchronization.

### actor
**Required** The name of the process issuing the request.

### payload
The data that are shared with every mate/participant when a sync match is made.
For example, if actor1's payload is payload1 and actor2's payload is payload2, both actors will receive the following JSON object when they sync together:

    {
      "payloads": {
        "acto1":"payload1",
        "acto2":"payload2"
      }
    }

### mates
The value of `mates` indicates the number of other processes
that are required to be present for an event to be synchronized. A value of 0 means no
synchronization, 1 means a pair of processes are required to be present, 3
and more means 4 or more processes are required to be present.

The default value is 1.

### selector
Selectors filter what events can or cannot be used for
synchronizing. E.g., an actor (a Netsync client) might only want to sync with
the events of a particular actor.
The formatting is identical to that of the [Labels and Selectors][k8s_labels_and_selectors] of Kubernetes.

The default selector is `actor != $MyActorName`, i.e., don't match me with another request from myself.

### labels
Labels are used by selectors (see above).
They are a comma separated list of `key1=value1,key2=value2` items.

The default label is `actor = $MyActorName`

## Using the Go API

```go
ns := netsync.NewNetsync()
defer ns.Close()

readyChan, err := ns.Send(netsync.Params{
  Actor: "player1",
  Event: "joinGame",
  Mates: 1,
  Labels: map[string]string{
          "id":   "1",
          "kind": "player",
  },
  Selector: "kind == player && id != 1",
  Payload: "I want to join a game with 1 other mate",
})

if err != nil {
  panic(err)
}

output := <-readyChan
```

## Background

This tool is inspired by the ideas in [CSP][csp_homepage] and Go unbuffered channels.
While it does provide features that are not available in other tools (e.g., allowing more than two clients to synchronize)
traditional services are probably better off with async, buffered message queues.
Feel free to let me know how you use Netsync in your project.
Issues and PRs are welcome. :)
