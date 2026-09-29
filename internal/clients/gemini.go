package clients

import (
	"context"
	"log"

	"golang.org/x/oauth2/google"
	"google.golang.org/genai"
	"google.golang.org/genai/interactions"
)

const project = "project-2d1aef35-db7e-45b1-b11"
const location = "global"

var (
	GeminiClient *genai.Client

	// InteractionsClient is built separately from GeminiClient on purpose.
	// genai.NewClient folds "projects/{p}/locations/{l}" into the Interactions
	// API's api_version *path parameter*, which is then percent-encoded, so the
	// request goes to /v1beta1%2Fprojects%2F.../interactions and 404s.
	//
	// Splitting the path so api_version is a single slash-free segment
	// ("global") produces the correct URL, and keeping it on its own client
	// leaves GeminiClient's BaseURL alone for Models calls (chunking.go).
	InteractionsClient *interactions.GenAI
)

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

	adcClient, err := google.DefaultClient(ctx, "https://www.googleapis.com/auth/cloud-platform")
	if err != nil {
		log.Fatalln("Failed to create ADC HTTP client", err)
	}
	InteractionsClient = interactions.New(
		interactions.WithServerURL("https://aiplatform.googleapis.com/v1beta1/projects/"+project+"/locations"),
		interactions.WithAPIVersion(location),
		interactions.WithClient(adcClient),
	)
}
