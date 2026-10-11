package gateway_test

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"

	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/models"
)

type latencySample struct {
	count uint64
	sum   float64
}

func gatewayLatencies(t *testing.T) map[string]latencySample {
	t.Helper()
	families, err := prometheus.DefaultGatherer.Gather()
	require.NoError(t, err)
	out := make(map[string]latencySample)
	for _, family := range families {
		name := strings.TrimPrefix(family.GetName(), "extproc_guardrails_")
		switch name {
		case "mask_duration_seconds", "demask_duration_seconds", "pipeline_duration_seconds", "sse_chunk_demask_duration_seconds":
			for _, metric := range family.GetMetric() {
				h := metric.GetHistogram()
				out[name] = latencySample{count: h.GetSampleCount(), sum: h.GetSampleSum()}
			}
		}
	}
	return out
}

func latencyDelta(before, after map[string]latencySample, name string) latencySample {
	return latencySample{count: after[name].count - before[name].count, sum: after[name].sum - before[name].sum}
}

func TestGatewayLatencyMetrics(t *testing.T) {
	// Keep these tests sequential: the service uses a process-wide registry.
	for _, tc := range []struct {
		name       string
		mode       models.GuardrailsMode
		body       string
		path       string
		disabled   bool
		emptyBody  bool
		maskErr    error
		wantMask   uint64
		wantDemask uint64
		wantSSE    bool
		emptySSE   bool
	}{
		{name: "enforce full", wantMask: 1, wantDemask: 1},
		{name: "enforce SSE", body: `{"stream":true,"messages":[{"role":"user","content":"alice@example.com"}]}`, wantMask: 1, wantSSE: true},
		{name: "SSE EOF only", body: `{"stream":true,"messages":[{"role":"user","content":"alice@example.com"}]}`, wantMask: 1, wantSSE: true, emptySSE: true},
		{name: "detect", mode: models.ModeDetect, wantMask: 1},
		{name: "no findings", body: `{"messages":[{"role":"user","content":"hello"}]}`, wantMask: 1},
		{name: "mask failure", maskErr: errors.New("scan failed"), wantMask: 1},
		{name: "unsupported schema", body: `{`, wantMask: 1},
		{name: "disabled", disabled: true},
		{name: "unguarded", path: "/other"},
		{name: "empty body", emptyBody: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tc.wantSSE {
					w.Header().Set("Content-Type", "text/event-stream")
					if !tc.emptySSE {
						_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"<EMAIL_1>\"}}]}\n\ndata: [DONE]\n\n")
					}
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"<EMAIL_1>"}}]}`)
			}))
			defer upstream.Close()
			settings := enforceSettings()
			settings.Enabled = !tc.disabled
			if tc.mode != "" {
				settings.Mode = tc.mode
			}
			masker := &fakeMasker{reps: []models.Replacement{emailRep()}, err: tc.maskErr}
			h := newHandler(t, upstream.URL, masker, settings, nil)
			path := tc.path
			if path == "" {
				path = "/v1/chat/completions"
			}
			body := tc.body
			if body == "" && !tc.emptyBody {
				body = `{"messages":[{"role":"user","content":"alice@example.com"}]}`
			}
			before := gatewayLatencies(t)
			resp := doPost(t, h, path, body, nil)
			require.NoError(t, resp.Body.Close())
			require.Equal(t, http.StatusOK, resp.StatusCode)
			after := gatewayLatencies(t)
			mask := latencyDelta(before, after, "mask_duration_seconds")
			demask := latencyDelta(before, after, "demask_duration_seconds")
			pipeline := latencyDelta(before, after, "pipeline_duration_seconds")
			sse := latencyDelta(before, after, "sse_chunk_demask_duration_seconds")
			require.Equal(t, tc.wantMask, mask.count, "mask observations")
			require.Equal(t, tc.wantMask, pipeline.count, "pipeline observations")
			require.Equal(t, tc.wantDemask, demask.count, "full-body demask observations")
			if tc.emptySSE {
				require.EqualValues(t, 1, sse.count, "EOF-only processing must be observed")
			} else if tc.wantSSE {
				require.Positive(t, sse.count, "SSE processing observations, including end-of-stream")
			} else {
				require.Zero(t, sse.count)
			}
			if tc.wantMask > 0 {
				require.Positive(t, mask.sum)
			}
			// Pipeline is processing time, not the elapsed upstream round trip.
			require.InDelta(t, mask.sum+demask.sum+sse.sum, pipeline.sum, 1e-9)
		})
	}
}

func TestGatewayLatencyOnUpstreamFailure(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	upstream.Close()
	masker := &fakeMasker{reps: []models.Replacement{emailRep()}}
	h := newHandler(t, upstream.URL, masker, enforceSettings(), nil)
	before := gatewayLatencies(t)
	resp := doPost(t, h, "/v1/chat/completions", `{"messages":[{"role":"user","content":"alice@example.com"}]}`, nil)
	require.NoError(t, resp.Body.Close())
	require.Equal(t, http.StatusBadGateway, resp.StatusCode)
	after := gatewayLatencies(t)
	mask := latencyDelta(before, after, "mask_duration_seconds")
	pipeline := latencyDelta(before, after, "pipeline_duration_seconds")
	require.EqualValues(t, 1, mask.count)
	require.EqualValues(t, 1, pipeline.count)
	require.InDelta(t, mask.sum, pipeline.sum, 1e-9)
	require.Zero(t, latencyDelta(before, after, "demask_duration_seconds").count)
	require.Zero(t, latencyDelta(before, after, "sse_chunk_demask_duration_seconds").count)
}

type delayedBody struct {
	reader io.Reader
	delay  time.Duration
}

func (b *delayedBody) Read(p []byte) (int, error) {
	time.Sleep(b.delay)
	return b.reader.Read(p)
}

func (b *delayedBody) Close() error { return nil }

type delayedWriter struct {
	*httptest.ResponseRecorder
	delay time.Duration
}

func (w *delayedWriter) Write(p []byte) (int, error) {
	time.Sleep(w.delay)
	return w.ResponseRecorder.Write(p)
}

func (w *delayedWriter) Flush() {
	time.Sleep(w.delay)
	w.ResponseRecorder.Flush()
}

func TestGatewayLatencyExcludesIO(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		name := "full"
		if streaming {
			name = "SSE"
		}
		t.Run(name, func(t *testing.T) {
			const ioDelay = 150 * time.Millisecond
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				time.Sleep(ioDelay) // Time to response headers.
				if streaming {
					w.Header().Set("Content-Type", "text/event-stream")
				} else {
					w.Header().Set("Content-Type", "application/json")
				}
				w.WriteHeader(http.StatusOK)
				w.(http.Flusher).Flush()
				time.Sleep(ioDelay) // Time waiting for the model's response body.
				if streaming {
					_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"<EMAIL_1>\"}}]}\n\ndata: [DONE]\n\n")
				} else {
					_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"<EMAIL_1>"}}]}`)
				}
			}))
			defer upstream.Close()
			masker := &fakeMasker{reps: []models.Replacement{emailRep()}}
			h := newHandler(t, upstream.URL, masker, enforceSettings(), nil)
			body := `{"messages":[{"role":"user","content":"alice@example.com"}]}`
			if streaming {
				body = `{"stream":true,"messages":[{"role":"user","content":"alice@example.com"}]}`
			}
			req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
			req.Body = &delayedBody{reader: strings.NewReader(body), delay: ioDelay}
			writer := &delayedWriter{ResponseRecorder: httptest.NewRecorder(), delay: ioDelay}
			before := gatewayLatencies(t)
			h.ServeHTTP(writer, req)
			after := gatewayLatencies(t)
			require.Equal(t, http.StatusOK, writer.Code)
			require.Contains(t, writer.Body.String(), "alice@example.com")
			pipeline := latencyDelta(before, after, "pipeline_duration_seconds")
			require.EqualValues(t, 1, pipeline.count)
			// Any one of the injected I/O waits would exceed this bound.
			require.Less(t, pipeline.sum, ioDelay.Seconds(), "pipeline must exclude request reads, upstream I/O and client writes")
		})
	}
}
