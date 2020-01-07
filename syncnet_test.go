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

func paramsToURL(p syncnet.Params) string {
	return fmt.Sprintf("/event?event=%s&actor=%s&payload=%s&selector=%s",
		url.QueryEscape(p.Event),
		url.QueryEscape(p.Actor),
		url.QueryEscape(p.Payload),
		url.QueryEscape(p.Selector),
	)
}

func TestBasic(t *testing.T) {
	requests := []syncnet.Params{
		{
			Event:   "e",
			Actor:   "a1",
			Payload: "v1",
		},
		{
			Event:   "e",
			Actor:   "a2",
			Payload: "v2",
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

			require.Equal(t, 2, len(outValue.Payloads))
			require.Equal(t, "v1", outValue.Payloads["a1"])
			require.Equal(t, "v2", outValue.Payloads["a2"])
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
			Payload:    "v1",
			MateWanted: 2,
			Context:    ctx,
		},
		{
			Event:      "e",
			Actor:      "a2",
			Payload:    "v2",
			MateWanted: 2,
			Context:    ctx,
		},
		{
			Event:      "e",
			Actor:      "a3",
			Payload:    "v3",
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
				require.Equal(t, 3, len(outValue.Payloads))
				require.Equal(t, "v1", outValue.Payloads["a1"])
				require.Equal(t, "v2", outValue.Payloads["a2"])
				require.Equal(t, "v3", outValue.Payloads["a3"])
			case <-ctx.Done():
			}

		}(p)
	}
	wg.Wait()
}

func TestHttpBasic(t *testing.T) {
	ts := httptest.NewServer(syncnet.NewSyncnet().NewHandler())
	defer ts.Close()

	requests := []syncnet.Params{
		{
			Event:   "e",
			Actor:   "a1",
			Payload: "v1",
		},
		{
			Event:   "e",
			Actor:   "a2",
			Payload: "v2",
		},
	}

	client := ts.Client()
	wg := sync.WaitGroup{}
	for _, p := range requests {
		wg.Add(1)
		go func(p syncnet.Params) {
			defer wg.Done()

			resp, err := client.Get(ts.URL + paramsToURL(p))
			require.NoError(t, err)

			require.Equal(t, 200, resp.StatusCode)

			body, err := ioutil.ReadAll(resp.Body)
			require.NoError(t, err)
			resp.Body.Close()

			outValue := syncnet.OutValue{}
			err = json.Unmarshal(body, &outValue)
			require.NoError(t, err)

			require.Equal(t, 2, len(outValue.Payloads))
			require.Equal(t, "v1", outValue.Payloads["a1"])
			require.Equal(t, "v2", outValue.Payloads["a2"])
		}(p)
	}
	wg.Wait()
}

func TestHttpMustBlock(t *testing.T) {
	ts := httptest.NewServer(syncnet.NewSyncnet().NewHandler())
	defer ts.Close()

	requests := []syncnet.Params{
		{
			Event:   "e",
			Actor:   "a", // same actor
			Payload: "v",
		},
		{
			Event:   "e",
			Actor:   "a", // same actor
			Payload: "v",
		},
	}

	client := ts.Client()
	wg := sync.WaitGroup{}
	for _, p := range requests {
		wg.Add(1)
		go func(p syncnet.Params) {
			defer wg.Done()

			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()

			httpReq, err := http.NewRequestWithContext(ctx, "GET", ts.URL+paramsToURL(p), nil)
			require.NoError(t, err)

			_, err = client.Do(httpReq)
			require.Error(t, err)
			require.Error(t, ctx.Err())
		}(p)
	}
	wg.Wait()
}

func TestHttpMustBlockBecauseOfSelector(t *testing.T) {
	ts := httptest.NewServer(syncnet.NewSyncnet().NewHandler())
	defer ts.Close()

	requests := []syncnet.Params{
		{
			Event:    "e",
			Actor:    "a1",
			Payload:  "v",
			Selector: "actor != a2", // a1 doesn't like a2
		},
		{
			Event:    "e",
			Actor:    "a2",
			Payload:  "v",
			Selector: "actor != a2", // but a2 has no problem with a1
		},
	}

	client := ts.Client()
	wg := sync.WaitGroup{}
	for _, p := range requests {
		wg.Add(1)

		// this sleep is here to ensure a1 sends it's request first, so
		// we can test if a1's selector is applied as well as a2's
		// selector
		time.Sleep(50 * time.Millisecond)
		go func(p syncnet.Params) {
			defer wg.Done()

			ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
			defer cancel()

			httpReq, err := http.NewRequestWithContext(ctx, "GET", ts.URL+paramsToURL(p), nil)
			require.NoError(t, err)

			_, err = client.Do(httpReq)
			require.Error(t, err)
			require.Error(t, ctx.Err())
		}(p)
	}
	wg.Wait()
}
