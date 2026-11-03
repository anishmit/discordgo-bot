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
	"google.golang.org/genai/interactions/models/components"
)

const apiVersion = "v1beta"
const maxLoggedBody = 4096

var (
	GeminiClient       *genai.Client
	InteractionsClient *interactions.GenAI
)

type streamProbeClient struct {
	inner *http.Client
}

func (c streamProbeClient) Do(req *http.Request) (*http.Response, error) {
	res, err := c.inner.Do(req)
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
	apiKey := os.Getenv("GEMINI_API_KEY")

	var err error
	GeminiClient, err = genai.NewClient(context.Background(), &genai.ClientConfig{
		APIKey:  apiKey,
		Backend: genai.BackendGeminiAPI,
	})
	if err != nil {
		log.Fatalln("Failed to create genai client", err)
	}

	InteractionsClient = interactions.New(
		interactions.WithAPIVersion(apiVersion),
		interactions.WithSecurity(components.Security{APIKey: &apiKey}),
		interactions.WithClient(streamProbeClient{inner: &http.Client{}}),
	)
}
