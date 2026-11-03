package clients

import (
	"bytes"
	"context"
	"io"
	"log"
	"net/http"
	"os"
	"strings"

	"google.golang.org/genai"
	"google.golang.org/genai/interactions"
)

const project = "project-df08581e-4836-4320-9ae"
const location = "global"
const maxLoggedBody = 4096

var (
	GeminiClient       *genai.Client
	InteractionsClient *interactions.Interactions
)

type probeTransport struct {
	inner http.RoundTripper
}

func (t probeTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	res, err := t.inner.RoundTrip(req)
	if err != nil || res == nil {
		return res, err
	}
	contentType := res.Header.Get("Content-Type")
	if strings.Contains(contentType, "text/event-stream") {
		return res, nil
	}
	if res.StatusCode == http.StatusOK && req.Method != http.MethodPost {
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
	log.Printf("Interactions %s %s returned %d %q: %s", req.Method, req.URL.String(), res.StatusCode, contentType, shown)
	res.Body = io.NopCloser(bytes.NewReader(body))
	return res, nil
}

func init() {
	ctx := context.Background()

	var err error
	GeminiClient, err = genai.NewClient(ctx, &genai.ClientConfig{
		Project:  project,
		Location: location,
		Backend:  genai.BackendEnterprise,
	})
	if err != nil {
		log.Fatalln("Failed to create genai client", err)
	}

	expressClient, err := genai.NewClient(ctx, &genai.ClientConfig{
		APIKey:     os.Getenv("GEMINI_API_KEY"),
		Backend:    genai.BackendEnterprise,
		HTTPClient: &http.Client{Transport: probeTransport{inner: http.DefaultTransport}},
	})
	if err != nil {
		log.Fatalln("Failed to create interactions client", err)
	}
	InteractionsClient = expressClient.Interactions
}
