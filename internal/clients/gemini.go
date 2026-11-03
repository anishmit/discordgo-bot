package clients

import (
	"bytes"
	"context"
	"io"
	"log"
	"net/http"
	"strings"

	"google.golang.org/genai"
	"google.golang.org/genai/interactions"
	"google.golang.org/genai/interactions/retry"
)

const project = "project-df08581e-4836-4320-9ae"
const location = "global"
const maxLoggedBody = 4096

type streamProbeClient struct {
	inner *http.Client
}

func (c streamProbeClient) Do(req *http.Request) (*http.Response, error) {
	res, err := c.inner.Do(req)
	if err != nil || res == nil {
		return res, err
	}
	contentType := res.Header.Get("Content-Type")
	if req.Method != http.MethodPost || strings.Contains(contentType, "text/event-stream") {
		return res, nil
	}
	body, readErr := io.ReadAll(res.Body)
	res.Body.Close()
	if readErr != nil {
		return nil, readErr
	}
	shown := body
	if len(shown) > maxLoggedBody {
		shown = shown[:maxLoggedBody]
	}
	log.Printf("Interactions %s %s returned %d %q instead of a stream: %s", req.Method, req.URL.Path, res.StatusCode, contentType, shown)
	res.Body = io.NopCloser(bytes.NewReader(body))
	return res, nil
}

var (
	GeminiClient       *genai.Client
	InteractionsClient *interactions.GenAI
)

func init() {
	ctx := context.Background()

	config := &genai.ClientConfig{
		Project:  project,
		Location: location,
		Backend:  genai.BackendEnterprise,
	}
	var err error
	GeminiClient, err = genai.NewClient(ctx, config)
	if err != nil {
		log.Fatalln("Failed to create genai client", err)
	}

	InteractionsClient = interactions.New(
		interactions.WithServerURL("https://aiplatform.googleapis.com/v1beta1/projects/"+project+"/locations"),
		interactions.WithAPIVersion(location),
		interactions.WithClient(streamProbeClient{inner: config.HTTPClient}),
		interactions.WithRetryConfig(retry.Config{
			Strategy: "attempt-count-backoff",
			Backoff: &retry.BackoffStrategy{
				InitialInterval: 1000,
				MaxInterval:     8000,
				Exponent:        2,
			},
			RetryConnectionErrors: true,
			MaxRetries:            genai.Ptr(5),
		}),
	)
}
