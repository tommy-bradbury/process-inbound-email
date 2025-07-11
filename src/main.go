package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"os"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/s3"
)

func handleRequest(ctx context.Context, sesEvent events.SimpleEmailEvent) error {
	sess, err := session.NewSession(&aws.Config{
		Region: aws.String("eu-west-1"),
	})
	if err != nil {
		return fmt.Errorf("failed to create AWS session: %w", err)
	}
	s3Client := s3.New(sess)

	for _, record := range sesEvent.Records {
		sesMail := record.SES.Mail
		sesReceipt := record.SES.Receipt

		log.Printf("[%s - %s] Mail = %+v, Receipt = %+v\n", record.EventVersion, record.EventSource, sesMail.MessageID, sesReceipt)

		bucket := "databater-emails-recieved"
		key := sesMail.MessageID

		if key == "" {
			log.Printf("email key empty for S3 action. MessageId: %s", sesMail.MessageID)
			continue
		}

		getObjectInput := &s3.GetObjectInput{
			Bucket: aws.String(bucket),
			Key:    aws.String(key),
		}

		result, err := s3Client.GetObject(getObjectInput)
		if err != nil {
			return fmt.Errorf("failed to get object %s::%s. Error: %w", bucket, key, err)
		}
		defer result.Body.Close()

		rawEmailBytes, err := io.ReadAll(result.Body)
		if err != nil {
			return fmt.Errorf("failed to read raw email from S3: %w", err)
		}

		msg, err := ParseEmailBody(bytes.NewReader(rawEmailBytes))

		if err != nil {
			return fmt.Errorf("parse email error: %w", err)
		}

		log.Printf("Subject: %v\n", msg.Subject)
		log.Printf("From: %v\n", msg.From)
		log.Printf("To: %v\n", msg.To)
		log.Printf("Message: %v\n", msg.PlainText)

		log.Printf("now finna do an openAI testTING")

		assistantID := os.Getenv("ASSISTANT_PRODUCT_PICKER")
		if assistantID == "" {
			log.Fatal("Error: ASSISTANT_PRODUCT_PICKER environment variable not set. Please set it to your OpenAI Assistant ID.")
		}

		openAIKey, err := GetOpenAICredential()
		if err != nil {
			log.Fatalf("Failed to get OPEN_AI_CREDENTIAL: %v", err)
		}
		initialThreadID := ""
		configOptions := 0 // Default: log errors, create new thread
		if initialThreadID != "" {
			configOptions |= RecallThreadID
		}

		assistant, err := NewAssistant(openAIKey, assistantID, configOptions, initialThreadID)
		if err != nil {
			log.Fatalf("Failed to initialize OpenAI Assistant: %v", err)
		}

		log.Printf("Assistant initialized. Using Thread ID: %s\n", assistant.GetThreadID())
		log.Printf("\nUser: %s\n", msg.PlainText)

		reply, err := assistant.AddMessageToThread(msg.PlainText)
		if err != nil {
			log.Fatalf("Failed to get reply from assistant: %v", err)
		}

		log.Printf("Assistant reckons the product required is: %s\n", reply)

	}

	return nil
}

func main() {
	lambda.Start(handleRequest)
}
