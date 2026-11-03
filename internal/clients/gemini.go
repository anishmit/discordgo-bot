package clients

import (
	"context"
	"log"
	"os"

	"google.golang.org/genai"
	"google.golang.org/genai/interactions"
	"google.golang.org/genai/interactions/models/components"
)

const apiVersion = "v1beta"

var (
	GeminiClient       *genai.Client
	InteractionsClient *interactions.GenAI
)

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
	)
}
