# Netsync

[![GoDoc](https://godoc.org/github.com/siadat/netsync?status.svg)][godoc]
[![Build Status](https://travis-ci.org/siadat/netsync.svg?branch=master)][travis]

[godoc]:  https://godoc.org/github.com/siadat/netsync
[travis]: https://travis-ci.org/siadat/netsync
[csp_homepage]: http://www.usingcsp.com/
[k8s_labels_and_selectors]: https://kubernetes.io/docs/concepts/overview/working-with-objects/labels/

Netsync is a synchronized messaging system.
It is a tool for synchronoizing processes over network via HTTP requests.
It also provides a Go API ([Go docs][godoc]) which could be used to synchronize Go routines.

## What does it do?

One process sends an HTTP request, and it is blocked until N
other matching processes send N other request with the same "event" query value.
The requests might have a *payload* value, which is shared with all requests in
the responses they eventually receive upon syncing.
Matching of requests can be limited using *labels* and *selectors*.

## Quick start: sync 2 processes

Start the server:

```bash
go install github.com/siadat/netsync/cmd/netsync
netsync :8000
```

Run the following on a terminal:

```bash
curl "http://localhost:8000/event?event=e&payload=v1&actor=actor1"
```

On another terminal, run:

```bash
curl "http://localhost:8000/event?event=e&payload=v2&actor=actor2"
```

Both of these will receive the following JSON response:
```json
{
  "payloads": {
    "actor2": "v2",
    "actor1": "v1"
  }
}
```

Note that these two requests matched and synced together because the following conditions are met:

- :white_check_mark: **event name**: The event names are identical, i.e., `e`.
- :white_check_mark: **selector 1 and label 2 match**: 1st request's selector (default: `actor != actor1`) matches 2nd request's label (default: `actor = actor2`).
- :white_check_mark: **selector 2 and label 1 match**: 2nd request's selector (default: `actor != actor2`) matches 1st request's label (default: `actor = actor1`).
- :white_check_mark: **number of mates**: Each request is asking for 1 other request to sync (because the default value of `mates` is 1) and there are already 2 requests.

## Example: sync 3 processes

With `mates=2` the request is blocked until 2 other requests are made.

```bash
curl "http://localhost:8000/event?event=e&payload=v1&actor=a1&mates=2" &
curl "http://localhost:8000/event?event=e&payload=v2&actor=a2&mates=2" &
curl "http://localhost:8000/event?event=e&payload=v3&actor=a3&mates=2" &
```

## Detailed example

Suppose we have two programs running concurrently,
and these processes are required to synchronize on a event EVENT before proceeding.
The logs of these processes could look like this:

    time  Process 1  Process 2
    ----  ---------  ---------
    1     p1.log1    p2.log1
    2                p2.log2
    3     p1.log2       
    4     p1.EVENT   p2.log3
    5     p1.log3    p2.log4
    6                p2.EVENT
    7                p2.log5

Maybe Process 1 needs to read a file that should be first prepared by Process 2 before Process 1 can continue its execution.
In this case, EVENT is "file is ready".
We want this EVENT to be synchronized accross the two processes, so, the desired log should look like:

    time  Process 1  Process 2
    ----  ---------  ---------
    1     p1.log1    p2.log1
    2                p2.log2
    3     p1.log2       
    4   ┌ p1.EVENT   p2.log3
    5   │            p2.log4
    6   └─────────── p2.EVENT
    7     p1.log3    p2.log5

Note that Process 1 is blocked for 2 time units, i.e., p1.log3 is not executed until p2.EVENT is executed.

## More complex example (skip this if you want)

Suppose we have a system with two processes running concurrently: a vending machine (VM), and a customer (CUST).
In [CSP][csp_homepage] notation, they can be described as:

    VM = coin -> choc -> VM
    CUST = hungry -> coin -> choc -> eat -> CUST
    SYSTEM = CUST || VM

VM and CUST must synchronize on `coin` and `choc` events.
Using Netsync, we can write the processes in Bash, e.g. the code for VM might look like this:

```bash
curl "http://localhost:8000/event?actor=VM&mates=1&event=coin" # VM's 1st request
curl "http://localhost:8000/event?actor=VM&mates=1&event=choc" # VM's 2nd request
```

And the code for CUST could look like this:

```bash
curl "http://localhost:8000/event?actor=CUST&mates=1&event=coin" # CUST's 1st request
curl "http://localhost:8000/event?actor=CUST&mates=1&event=choc" # CUST's 2nd request
```

When run concurrently, the log of Netsync will look like this:

```
┌─ VM:coin            # 'coin' from VM
│┌ CUST:coin          # 'coin' from CUST
└└ CUST:coin VM:coin  # 'coin' requests are synced

┌─ VM:choc            # 'choc' from VM
│┌ CUST:choc          # 'choc' from CUST
└└ CUST:choc VM:choc  # 'choc' requests are synced
```

## Endpoints

There are 2 endpoints:

### /event

This is the main endpoint. Actors (ie clients) send events using this endpoint.

### /stats

This endpoint can be used to monitor the current pending/blocking requests waiting for a mate to sync.

## Options

An example of a request with all options set:

```bash
curl "http://localhost:8000/event?actor=CUST&mates=1&event=choc&labels=actor%3DCUST&selector=actor!%3DCUST&payload=value"
                                  ^          ^       ^          ^                   ^                      ^
                                  |          |       |          |                   |                      |
                                  actor=CUST |       |          |                   |                      |
                                             mates=1 |          |                   |                      |
                                                     event=choc |                   |                      |
                                                                labels=actor%3DCUST |                      |
                                                                       ^            |                      |
                                                                       |            |                      |
                                                                       actor=CUST   |                      |
                                                                                    selector=actor!%3DCUST |
                                                                                             ^             |
                                                                                             |             |
                                                                                             actor!=CUST   |
                                                                                                           |  
                                                                                                           |
                                                                                                           payload=value
```

### event=
**Required** The event key. Identical events are synchronized. See labels and
selectors for more fine-grained control over synchronization.

### actor=
**Required** The name of the process issuing the request.

### payload=
The data that are shared with every mate/participant when a sync match is made.
For example, if actor1's payload is payload1 and actor2's payload is payload2, both actors will receive the following JSON object when they sync together:

    {
      "payloads": {
        "acto1":"payload1",
        "acto2":"payload2"
      }
    }

### mates=
The value of `mates` indicates the number of other processes
that are required to be present for an event to be synchronized. A value of 0 means no
synchronization, 1 means a pair of processes are required to be present, 3
and more means 4 or more processes are required to be present.

The default value is 1.

### selector=
Selectors filter what events can or cannot be used for
synchronizing. E.g., an actor (a Netsync client) might only want to sync with
events of a particular actor.
The formatting is identical with [Kubernetes Labels and Selectors][k8s_labels_and_selectors].

The default selector is `actor!=$MyActorName`, i.e., don't match me with another request from myself.

### labels=
Labels are used by selectors (see above).
They are a comma separated list of `key1=value1,key2=value2` items.

The default label is `actor=$MyActorName`

## Using the Go API

```go
ns := netsync.NewNetsync()
defer ns.Close()

doneChan, err := ns.Send(netsync.Params{
  Actor: "CUST",
  Event: "choc",
  Payload: "Please give me a chocolate",
})

if err != nil {
  panic(err)
}

output := <-doneChan
```

## Background

This tool is inspired by the ideas in [CSP][csp_homepage] and Go unbuffered channels.
While it does provide features that are not available in other tools (e.g., allowing more than two clients to synchronize)
traditional services are probably better off with async, buffered message queues.
However, Netsync provides synchronization it the cases where it is required.
Feel free to let me know how you use Netsync in your project.
PRs (with tests) and issues are all welcome.
