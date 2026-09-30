package clients

import (
	"context"
	"log"

	"google.golang.org/genai"
	"google.golang.org/genai/interactions"
	"google.golang.org/genai/interactions/retry"
)

const project = "project-2d1aef35-db7e-45b1-b11"
const location = "global"

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
		interactions.WithClient(config.HTTPClient),
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
