package clients

import (
	"context"
	"log"

	"google.golang.org/genai"
)

const project = "project-df08581e-4836-4320-9ae"
const location = "global"

var GeminiClient *genai.Client

func init() {
	var err error
	GeminiClient, err = genai.NewClient(context.Background(), &genai.ClientConfig{
		Project:     project,
		Location:    location,
		Backend:     genai.BackendEnterprise,
		HTTPOptions: genai.HTTPOptions{RetryOptions: &genai.HTTPRetryOptions{}},
	})
	if err != nil {
		log.Fatalln("Failed to create genai client", err)
	}
}
