package syncnet_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/siadat/syncnet"
	"github.com/stretchr/testify/require"
)

func paramsToURL(p syncnet.Params) string {
	labels := make([]string, 0, len(p.Labels))
	for k, v := range p.Labels {
		labels = append(labels, strings.Join([]string{k, v}, "="))
	}

	return fmt.Sprintf("/event?event=%s&actor=%s&payload=%s&labels=%s&selector=%s",
		url.QueryEscape(p.Event),
		url.QueryEscape(p.Actor),
		url.QueryEscape(p.Payload),
		url.QueryEscape(strings.Join(labels, ",")),
		url.QueryEscape(p.Selector),
	)
}

func TestBasic(t *testing.T) {
	// t.Parallel()

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
	defer sn.Close()

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
	// t.Parallel()

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
	defer sn.Close()

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
	// t.Parallel()

	sn := syncnet.NewSyncnet()
	defer sn.Close()

	ts := httptest.NewServer(sn.NewHandler())
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
	// t.Parallel()

	sn := syncnet.NewSyncnet()
	defer sn.Close()

	ts := httptest.NewServer(sn.NewHandler())
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
	// t.Parallel()

	sn := syncnet.NewSyncnet()
	defer sn.Close()

	ts := httptest.NewServer(sn.NewHandler())
	defer ts.Close()

	testCases := []struct {
		wantBlock bool
		requests  []syncnet.Params
	}{
		{
			wantBlock: true,
			requests: []syncnet.Params{
				{
					Event:   "e",
					Actor:   "a1",
					Payload: "v",
					Labels:  map[string]string{"label1": "value1"},
				},
				{
					Event:    "e",
					Actor:    "a2",
					Payload:  "v",
					Selector: "label1 != value1", // a2 doesn't like events where label1=value1
				},
			},
		},
		{
			wantBlock: false,
			requests: []syncnet.Params{
				{
					Event:   "e",
					Actor:   "a1",
					Payload: "v",
					Labels:  map[string]string{"label1": "value1"},
				},
				{
					Event:    "e",
					Actor:    "a2",
					Payload:  "v",
					Selector: "label1 == value1", // a2 only syncs with label1=value1
				},
			},
		},
	}

	client := ts.Client()
	wg := sync.WaitGroup{}
	for _, tt := range testCases {
		for _, p := range tt.requests {
			wg.Add(1)

			// this sleep is here to ensure a1 sends it's request first, so
			// we can test if a1's selector is applied as well as a2's
			// selector
			time.Sleep(50 * time.Millisecond)
			go func(p syncnet.Params) {
				defer wg.Done()

				if tt.wantBlock {
					ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
					defer cancel()

					httpReq, err := http.NewRequestWithContext(ctx, "GET", ts.URL+paramsToURL(p), nil)
					require.NoError(t, err)

					_, err = client.Do(httpReq)
					require.Error(t, err)
					require.Error(t, ctx.Err())
				} else {
					ch, err := sn.Send(p)
					require.NoError(t, err)

					outValue := <-ch

					require.Equal(t, 2, len(outValue.Payloads))
					// require.Equal(t, "v1", outValue.Payloads["a1"])
					// require.Equal(t, "v2", outValue.Payloads["a2"])
				}
			}(p)
		}
		wg.Wait()
	}
}
