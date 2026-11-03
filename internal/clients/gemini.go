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
)

const project = "project-df08581e-4836-4320-9ae"
const location = "global"
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
	logCredentials(ctx, config)

	InteractionsClient = interactions.New(
		interactions.WithServerURL("https://aiplatform.googleapis.com/v1beta1/projects/"+project+"/locations"),
		interactions.WithAPIVersion(location),
		interactions.WithClient(streamProbeClient{inner: config.HTTPClient}),
	)
}

func logCredentials(ctx context.Context, config *genai.ClientConfig) {
	if config.Credentials == nil {
		log.Println("Gemini credentials: none resolved, requests will be unauthenticated")
		return
	}
	token, err := config.Credentials.Token(ctx)
	if err != nil {
		log.Println("Gemini credentials: failed to mint a token", err)
		return
	}
	quota, err := config.Credentials.QuotaProjectID(ctx)
	if err != nil {
		log.Println("Gemini credentials: failed to read the quota project", err)
	}
	log.Printf("Gemini credentials: token of %d bytes, quota project %q", len(token.Value), quota)
}
