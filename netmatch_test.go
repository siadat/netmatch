package netmatch_test

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

	"github.com/siadat/netmatch"
	"github.com/stretchr/testify/require"
)

func paramsToURL(p netmatch.Params) string {
	labels := make([]string, 0, len(p.Labels))
	for k, v := range p.Labels {
		labels = append(labels, strings.Join([]string{k, v}, "="))
	}

	return fmt.Sprintf("/match?key=%s&payload=%s&labels=%s&selector=%s",
		url.QueryEscape(p.Key),
		url.QueryEscape(p.Payload),
		url.QueryEscape(strings.Join(labels, ",")),
		url.QueryEscape(p.Selector),
	)
}

func TestBasic(t *testing.T) {
	t.Parallel()

	requests := []netmatch.Params{
		{
			Key:      "e",
			Labels:   map[string]string{"name": "a1"},
			Selector: "name != a1",
			Payload:  "v1",
			Count:    1,
		},
		{
			Key:      "e",
			Labels:   map[string]string{"name": "a2"},
			Selector: "name != a2",
			Payload:  "v2",
			Count:    1,
		},
	}

	nm := netmatch.NewNetmatch()
	defer nm.Close()

	wg := sync.WaitGroup{}
	for _, p := range requests {
		wg.Add(1)
		go func(p netmatch.Params) {
			defer wg.Done()

			ch, err := nm.Match(p)
			require.NoError(t, err)

			outValue := <-ch

			require.Equal(t, len(requests), len(outValue.Requests), fmt.Sprintf("%+v", outValue))
			require.Contains(t, outValue.Requests, netmatch.MatchValueItem{Labels: map[string]string{"name": "a1"}, Payload: "v1"})
			require.Contains(t, outValue.Requests, netmatch.MatchValueItem{Labels: map[string]string{"name": "a2"}, Payload: "v2"})
		}(p)
	}
	wg.Wait()
}

func TestBasicTriplet(t *testing.T) {
	t.Parallel()

	ctx, _ := context.WithTimeout(context.Background(), 500*time.Millisecond)
	requests := []netmatch.Params{
		{
			Key:      "e",
			Labels:   map[string]string{"name": "a1"},
			Selector: "name != a1",
			Payload:  "v1",
			Count:    2,
			Context:  ctx,
		},
		{
			Key:      "e",
			Labels:   map[string]string{"name": "a2"},
			Selector: "name != a1",
			Payload:  "v2",
			Count:    2,
			Context:  ctx,
		},
		{
			Key:      "e",
			Labels:   map[string]string{"name": "a3"},
			Selector: "name != a1",
			Payload:  "v3",
			Count:    2,
			Context:  ctx,
		},
	}

	nm := netmatch.NewNetmatch()
	defer nm.Close()

	wg := sync.WaitGroup{}
	for _, p := range requests {
		wg.Add(1)
		go func(p netmatch.Params) {
			defer wg.Done()

			ch, err := nm.Match(p)
			require.NoError(t, err)

			select {
			case outValue := <-ch:
				require.Equal(t, len(requests), len(outValue.Requests), fmt.Sprintf("%+v", outValue))
				require.Contains(t, outValue.Requests, netmatch.MatchValueItem{Labels: map[string]string{"name": "a1"}, Payload: "v1"})
				require.Contains(t, outValue.Requests, netmatch.MatchValueItem{Labels: map[string]string{"name": "a2"}, Payload: "v2"})
				require.Contains(t, outValue.Requests, netmatch.MatchValueItem{Labels: map[string]string{"name": "a3"}, Payload: "v3"})
			case <-ctx.Done():
			}

		}(p)
	}
	wg.Wait()
}

func TestHttpBasic(t *testing.T) {
	t.Parallel()

	nm := netmatch.NewNetmatch()
	defer nm.Close()

	ts := httptest.NewServer(nm.NewHandler())
	defer ts.Close()

	requests := []netmatch.Params{
		{
			Key:      "e",
			Labels:   map[string]string{"name": "a1"},
			Selector: "name != a1",
			Payload:  "v1",
		},
		{
			Key:      "e",
			Labels:   map[string]string{"name": "a2"},
			Selector: "name != a2",
			Payload:  "v2",
		},
	}

	client := ts.Client()
	wg := sync.WaitGroup{}
	for _, p := range requests {
		wg.Add(1)
		go func(p netmatch.Params) {
			defer wg.Done()

			resp, err := client.Get(ts.URL + paramsToURL(p))
			require.NoError(t, err)

			require.Equal(t, 200, resp.StatusCode)

			body, err := ioutil.ReadAll(resp.Body)
			require.NoError(t, err)
			resp.Body.Close()

			outValue := netmatch.MatchValue{}
			err = json.Unmarshal(body, &outValue)
			require.NoError(t, err)

			require.Equal(t, len(requests), len(outValue.Requests), fmt.Sprintf("%+v", outValue))
			require.Contains(t, outValue.Requests, netmatch.MatchValueItem{Labels: map[string]string{"name": "a1"}, Payload: "v1"})
			require.Contains(t, outValue.Requests, netmatch.MatchValueItem{Labels: map[string]string{"name": "a2"}, Payload: "v2"})
		}(p)
	}
	wg.Wait()
}

func TestHttpMustBlock(t *testing.T) {
	t.Parallel()

	nm := netmatch.NewNetmatch()
	defer nm.Close()

	ts := httptest.NewServer(nm.NewHandler())
	defer ts.Close()

	requests := []netmatch.Params{
		{
			Key:      "e",
			Labels:   map[string]string{"name": "a"}, // same actor
			Selector: "name != a",
			Payload:  "v",
		},
		{
			Key:      "e",
			Labels:   map[string]string{"name": "a"}, // same actor
			Selector: "name != a",
			Payload:  "v",
		},
	}

	client := ts.Client()
	wg := sync.WaitGroup{}
	for _, p := range requests {
		wg.Add(1)
		go func(p netmatch.Params) {
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
	t.Parallel()

	nm := netmatch.NewNetmatch()
	defer nm.Close()

	ts := httptest.NewServer(nm.NewHandler())
	defer ts.Close()

	testCases := []struct {
		wantBlock bool
		requests  []netmatch.Params
	}{
		{
			wantBlock: true,
			requests: []netmatch.Params{
				{
					Key:      "e",
					Labels:   map[string]string{"name": "a1", "label1": "value1"},
					Selector: "name != a1",
					Payload:  "v",
					Count:    1,
				},
				{
					Key:      "e",
					Labels:   map[string]string{"name": "a2"},
					Payload:  "v",
					Count:    1,
					Selector: "label1 != value1", // a2 doens't like requests where label1=value1
				},
			},
		},
		{
			wantBlock: false,
			requests: []netmatch.Params{
				{
					Key:      "e",
					Labels:   map[string]string{"name": "a1", "label1": "value1"},
					Selector: "name != a1",
					Payload:  "v",
					Count:    1,
				},
				{
					Key:      "e",
					Labels:   map[string]string{"name": "a2"},
					Payload:  "v",
					Count:    1,
					Selector: "label1 == value1", // a2 only matches with label1=value1
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
			go func(p netmatch.Params) {
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
					ch, err := nm.Match(p)
					require.NoError(t, err)

					outValue := <-ch

					require.Equal(t, len(tt.requests), len(outValue.Requests), fmt.Sprintf("%+v", outValue))
				}
			}(p)
		}
		wg.Wait()
	}
}

func TestCount(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		wantBlock bool
		requests  []netmatch.Params
	}{
		{
			wantBlock: false,
			requests: []netmatch.Params{
				{
					Key:      "e",
					Labels:   map[string]string{"name": "a1"},
					Selector: "name != a1",
					Payload:  "v",
					Count:    2,
				},
				{
					Key:      "e",
					Labels:   map[string]string{"name": "a2"},
					Selector: "name != a2",
					Payload:  "v",
					Count:    2,
				},
				{
					Key:      "e",
					Labels:   map[string]string{"name": "a3"},
					Selector: "name != a3",
					Payload:  "v",
					Count:    2,
				},
			},
		},
		{
			wantBlock: true,
			requests: []netmatch.Params{
				{
					Key:      "e",
					Labels:   map[string]string{"name": "a1"},
					Selector: "name != a1",
					Payload:  "v",
					Count:    2,
				},
				{
					Key:      "e",
					Labels:   map[string]string{"name": "a2"},
					Selector: "name != a2",
					Payload:  "v",
					Count:    2,
				},
			},
		},
		{
			wantBlock: false,
			requests: []netmatch.Params{
				{
					Key:      "e",
					Labels:   map[string]string{"name": "a1"},
					Selector: "name != a1",
					Payload:  "v",
					Count:    1,
				},
				{
					Key:      "e",
					Labels:   map[string]string{"name": "a2"},
					Selector: "name != a2",
					Payload:  "v",
					Count:    1,
				},
			},
		},
		{
			wantBlock: false,
			requests: []netmatch.Params{
				{
					Key:      "e",
					Labels:   map[string]string{"name": "a1"},
					Selector: "name != a1",
					Payload:  "v",
					Count:    0,
				},
			},
		},
	}

	wg := sync.WaitGroup{}
	for _, tt := range testCases {
		func() {
			nm := netmatch.NewNetmatch()
			for i, p := range tt.requests {
				wg.Add(1)

				// this sleep is here to ensure a1 sends it's request first, so
				// we can test if a1's selector is applied as well as a2's
				// selector
				time.Sleep(50 * time.Millisecond)
				go func(i int, p netmatch.Params) {
					defer wg.Done()

					if tt.wantBlock {
						ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
						defer cancel()

						p.Context = ctx

						ch, err := nm.Match(p)
						require.NoError(t, err)

						select {
						case <-ch:
						case <-p.Context.Done():
						}

						require.Error(t, p.Context.Err())
					} else {
						ch, err := nm.Match(p)
						require.NoError(t, err)

						outValue := <-ch

						require.Equal(t, len(tt.requests), len(outValue.Requests), fmt.Sprintf("%+v", outValue))
					}
				}(i, p)
			}
			wg.Wait()
			nm.Close()
		}()
	}
}
