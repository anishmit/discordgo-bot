package clients

import (
	"bytes"
	"context"
	"encoding/json"
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
	if res.StatusCode >= 400 {
		if normalized, ok := normalizeErrorBody(body); ok {
			body = normalized
		}
	}
	res.ContentLength = int64(len(body))
	res.Body = io.NopCloser(bytes.NewReader(body))
	return res, nil
}

func normalizeErrorBody(body []byte) ([]byte, bool) {
	raw := bytes.TrimSpace(body)
	if bytes.HasPrefix(raw, []byte("[")) {
		var elements []json.RawMessage
		if err := json.Unmarshal(raw, &elements); err != nil || len(elements) != 1 {
			return nil, false
		}
		raw = elements[0]
	}
	var payload struct {
		Error struct {
			Message string `json:"message"`
			Status  string `json:"status"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil || payload.Error.Message == "" {
		return nil, false
	}
	normalized, err := json.Marshal(map[string]map[string]string{
		"error": {"code": payload.Error.Status, "message": payload.Error.Message},
	})
	if err != nil {
		return nil, false
	}
	return normalized, true
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
