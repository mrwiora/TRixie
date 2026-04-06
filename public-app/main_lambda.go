//go:build dynamodb

package main

import (
	"log"

	"github.com/aws/aws-lambda-go/lambda"
	"github.com/awslabs/aws-lambda-go-api-proxy/httpadapter"
)

func main() {
	store, err := NewDynamoDBStore()
	if err != nil {
		log.Fatal(err)
	}

	mux := initHandlers(store)

	log.Printf("TRixie Lambda starting")
	log.Printf("Version: %s, Commit: %s, Built: %s", Version, GitCommit, BuildTime)
	lambda.Start(httpadapter.NewV2(mux).ProxyWithContext)
}
