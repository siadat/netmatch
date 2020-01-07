package syncnet_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/siadat/syncnet"
	"github.com/stretchr/testify/require"
)

type testRequestStruct struct {
	actor    string
	event    string
	value    string
	selector string
}

func (req *testRequestStruct) URL() string {
	return fmt.Sprintf("/event?event=%s&actor=%s&value=%s&selector=%s",
		url.QueryEscape(req.event),
		url.QueryEscape(req.actor),
		url.QueryEscape(req.value),
		url.QueryEscape(req.selector),
	)
}

func TestBasic(t *testing.T) {
	requests := []syncnet.Params{
		{
			Event: "e",
			Actor: "a1",
			Value: "v1",
		},
		{
			Event: "e",
			Actor: "a2",
			Value: "v2",
		},
	}

	sn := syncnet.NewSyncnet()
	wg := sync.WaitGroup{}
	for _, p := range requests {
		wg.Add(1)
		go func(p syncnet.Params) {
			defer wg.Done()

			ch, err := sn.Send(p)
			require.NoError(t, err)

			outValue := <-ch

			require.Equal(t, 2, len(outValue.Values))
			require.Equal(t, "v1", outValue.Values["a1"])
			require.Equal(t, "v2", outValue.Values["a2"])
		}(p)
	}
	wg.Wait()
}

func TestBasicTriplet(t *testing.T) {
	ctx, _ := context.WithTimeout(context.Background(), 500*time.Millisecond)
	requests := []syncnet.Params{
		{
			Event:      "e",
			Actor:      "a1",
			Value:      "v1",
			MateWanted: 2,
			Context:    ctx,
		},
		{
			Event:      "e",
			Actor:      "a2",
			Value:      "v2",
			MateWanted: 2,
			Context:    ctx,
		},
		{
			Event:      "e",
			Actor:      "a3",
			Value:      "v3",
			MateWanted: 2,
			Context:    ctx,
		},
	}

	sn := syncnet.NewSyncnet()
	wg := sync.WaitGroup{}
	for _, p := range requests {
		wg.Add(1)
		go func(p syncnet.Params) {
			defer wg.Done()

			ch, err := sn.Send(p)
			require.NoError(t, err)

			select {
			case outValue := <-ch:
				require.Equal(t, 3, len(outValue.Values))
				require.Equal(t, "v1", outValue.Values["a1"])
				require.Equal(t, "v2", outValue.Values["a2"])
				require.Equal(t, "v3", outValue.Values["a3"])
			case <-ctx.Done():
			}

		}(p)
	}
	wg.Wait()
}

func TestHttpBasic(t *testing.T) {
	ts := httptest.NewServer(syncnet.NewSyncnet().NewHandler())
	defer ts.Close()

	requests := []testRequestStruct{
		{
			event: "e",
			actor: "a1",
			value: "v1",
		},
		{
			event: "e",
			actor: "a2",
			value: "v2",
		},
	}

	client := ts.Client()
	wg := sync.WaitGroup{}
	for _, req := range requests {
		wg.Add(1)
		go func(req testRequestStruct) {
			defer wg.Done()

			resp, err := client.Get(ts.URL + req.URL())
			require.NoError(t, err)

			require.Equal(t, 200, resp.StatusCode)

			body, err := ioutil.ReadAll(resp.Body)
			require.NoError(t, err)
			resp.Body.Close()

			outValue := syncnet.OutValue{}
			err = json.Unmarshal(body, &outValue)
			require.NoError(t, err)

			require.Equal(t, 2, len(outValue.Values))
			require.Equal(t, "v1", outValue.Values["a1"])
			require.Equal(t, "v2", outValue.Values["a2"])
		}(req)
	}
	wg.Wait()
}

func TestHttpMustBlock(t *testing.T) {
	ts := httptest.NewServer(syncnet.NewSyncnet().NewHandler())
	defer ts.Close()

	requests := []testRequestStruct{
		{
			event: "e",
			actor: "a", // same actor
			value: "v",
		},
		{
			event: "e",
			actor: "a", // same actor
			value: "v",
		},
	}

	client := ts.Client()
	wg := sync.WaitGroup{}
	for _, req := range requests {
		wg.Add(1)
		go func(req testRequestStruct) {
			defer wg.Done()

			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()

			httpReq, err := http.NewRequestWithContext(ctx, "GET", ts.URL+req.URL(), nil)
			require.NoError(t, err)

			_, err = client.Do(httpReq)
			require.Error(t, err)
			require.Error(t, ctx.Err())
		}(req)
	}
	wg.Wait()
}

func TestHttpMustBlockBecauseOfSelector(t *testing.T) {
	ts := httptest.NewServer(syncnet.NewSyncnet().NewHandler())
	defer ts.Close()

	requests := []testRequestStruct{
		{
			event:    "e",
			actor:    "a1",
			value:    "v",
			selector: "actor != a2", // a1 doesn't like a2
		},
		{
			event:    "e",
			actor:    "a2",
			value:    "v",
			selector: "actor != a2", // but a2 has no problem with a1
		},
	}

	client := ts.Client()
	wg := sync.WaitGroup{}
	for _, req := range requests {
		wg.Add(1)

		// this sleep is here to ensure a1 sends it's request first, so
		// we can test if a1's selector is applied as well as a2's
		// selector
		time.Sleep(50 * time.Millisecond)
		go func(req testRequestStruct) {
			defer wg.Done()

			ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
			defer cancel()

			httpReq, err := http.NewRequestWithContext(ctx, "GET", ts.URL+req.URL(), nil)
			require.NoError(t, err)

			_, err = client.Do(httpReq)
			require.Error(t, err)
			require.Error(t, ctx.Err())
		}(req)
	}
	wg.Wait()
}
