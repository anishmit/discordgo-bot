package clients

import (
	"context"
	"log"
	"os"
	"firebase.google.com/go/v4"
	"firebase.google.com/go/v4/db"
	"google.golang.org/api/option"
)

var FirebaseDBClient *db.Client

func init() {
	ctx := context.Background()
	conf := &firebase.Config{
		DatabaseURL: os.Getenv("FIREBASE_DB_URL"),
	}
	opt := option.WithCredentialsFile("serviceAccountKey.json")
	App, err := firebase.NewApp(ctx, conf, opt)
	if err != nil {
		log.Println("Error creating new Firebase app", err)
	}
	FirebaseDBClient, err = App.Database(ctx)
	if err != nil {
		log.Println("Error getting Firebase DB client", err)
	}
}