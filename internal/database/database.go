package database

import (
	"context"
	"log"

	"github.com/jackc/pgx/v5/pgxpool"
)

var Pool *pgxpool.Pool

func Init(connString string) {
	var err error
	Pool, err = pgxpool.New(context.Background(), connString)
	if err != nil {
		log.Fatalln("Error connecting to database", err)
	}
}
