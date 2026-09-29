package clients

import (
	"context"
	"log"
	"google.golang.org/genai"
)

const project = "project-2d1aef35-db7e-45b1-b11"
const location = "global"

var GeminiClient *genai.Client

func init() {
	ctx := context.Background()
	var err error
	GeminiClient, err = genai.NewClient(ctx, &genai.ClientConfig{
		Project:  project,
		Location: location,
		Backend: genai.BackendEnterprise,
	})
	if err != nil {
		log.Fatalln("Failed to create genai client", err)
	}
}