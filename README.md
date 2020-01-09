# syncnet
[![GoDoc](https://godoc.org/github.com/siadat/syncnet?status.svg)][godoc]
[![Build Status](https://travis-ci.org/siadat/syncnet.svg?branch=master)][travis]

Syncnet is a synchronized messaging system.
It is a tool for synchronoizing processes over network via HTTP requests.
It also provides a Go API ([Go docs][godoc]) which could be used to synchronize Go routines.

This is an experiment inspired by the ideas in [CSP][csp_homepage] and Go unbuffered channels.
While this tool does provide features that are not available in other tools (e.g., allowing more than two clients to synchronize)
most systems are probably better designed with async, buffered message queues.
With that in mind, let the fun begin!

[godoc]:  https://godoc.org/github.com/siadat/syncnet
[travis]: https://travis-ci.org/siadat/syncnet
[csp_homepage]: http://www.usingcsp.com/
[k8s_labels_and_selectors]: https://kubernetes.io/docs/concepts/overview/working-with-objects/labels/

## Quick start

    go install github.com/siadat/syncnet/cmd/syncnet
    syncnet :8000

## What does it do?

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

## Example

Suppose we have a system with two processes running concurrently: a vending machine (VM), and a customer (CUST).
In [CSP][csp_homepage] notation, they can be described as:

    VM = coin -> choc -> VM
    CUST = hungry -> coin -> choc -> eat -> CUST

    SYSTEM = CUST || VM

VM and CUST must synchronize on `coin` and `choc` events.
Using Syncnet, we can write the processes in Bash, e.g. the code for VM might look like this:

```bash
curl "http://localhost:8000/event?actor=VM&mates=1&event=coin" # VM's 1st request
curl "http://localhost:8000/event?actor=VM&mates=1&event=choc" # VM's 2nd request
```

And the code for CUST could look like this:

```bash
curl "http://localhost:8000/event?actor=CUST&mates=1&event=coin" # CUST's 1st request
curl "http://localhost:8000/event?actor=CUST&mates=1&event=choc" # CUST's 2nd request
```

When run concurrently, the log of Syncnet will look like this:

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

    curl "http://localhost:8000/event?actor=CUST&mates=1&event=choc&labels=actor%3DCUST&selector=actor!%3DCUST"
                                      ^          ^       ^          ^                   ^
                                      |          |       |          |                   |
                                      actor=CUST |       |          |                   |
                                                 mates=1 |          |                   |
                                                         event=choc |                   |
                                                                    labels=actor%3DCUST |
                                                                           ^            |
                                                                           |            |
                                                                           actor=CUST   |
                                                                                        selector=actor!%3DCUST
                                                                                                 ^
                                                                                                 |
                                                                                                 actor!=CUST

### event=
**Required** The event key. Identical events are synchronized. See labels and
selectors for more fine-grained control over synchronization.

### actor=
**Required** The name of the process issuing the request.

### mates=
The value of `mates` indicates the number of other processes
that are required to be present for an event to be synchronized. A value of 0 means no
synchronization, 1 means a pair of processes are required to be present, 3
and more means 4 or more processes are required to be present.

The default value is 1.

### selector=
Selectors filter what events can or cannot be used for
synchronizing. E.g., an actor (a Syncnet client) might only want to sync with
events of a particular actor.
The formatting is identical with [Kubernetes Labels and Selectors][k8s_labels_and_selectors].

The default selector is `actor!=$MyActorName`, i.e., don't match me with another request from myself.

### labels=
Labels are used by selectors (see above).
They are a comma separated list of `key1=value1,key2=value2` items.

The default label is `actor=$MyActorName`

## Example of using the Go API

```go
sn := syncnet.NewSyncnet()
defer sn.Close()

doneChan := sn.Send(syncnet.Params{
  Actor: "CUST",
  Event: "choc",
  Payload: "Please give me a chocolate",
})

output := <-doneChan
```
