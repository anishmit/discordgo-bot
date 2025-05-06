package clients

import (
	"context"
	"log"
	"os"
	"google.golang.org/genai"
)

var GeminiClient *genai.Client

func init() {
	ctx := context.Background()
	var err error
	GeminiClient, err = genai.NewClient(ctx, &genai.ClientConfig{
		APIKey: os.Getenv("GEMINI_API_KEY"),
		Backend: genai.BackendGeminiAPI,
	})
	if err != nil {
		log.Fatalln("Failed to create genai client", err)
	}
}