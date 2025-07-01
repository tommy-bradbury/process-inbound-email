package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"math"
	"net/mail"
	"strings"

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

		log.Printf("Attempting to fetch email from S3: Bucket=%s, Key=%s\n", bucket, key)

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

		previewLength := int(math.Min(float64(len(rawEmailBytes)), 500))
		log.Printf("Raw email fetched from S3 (first %d chars): %s\n", previewLength, rawEmailBytes[:previewLength])

		msg, err := mail.ReadMessage(bytes.NewReader(rawEmailBytes))
		if err != nil {
			return fmt.Errorf("parse email error: %w", err)
		}

		log.Printf("Subject: %v\n", msg.Header.Get("Subject"))
		log.Printf("From: %v\n", msg.Header.Get("From"))
		log.Printf("To: %v\n", msg.Header.Get("To"))

		bodyContent, err := io.ReadAll(msg.Body)
		if err != nil {
			return fmt.Errorf("failed to read email body: %w", err)
		}

		contentType := msg.Header.Get("Content-Type")
		log.Printf("Content-Type: %s\n", contentType)

		if len(bodyContent) > 0 {
			lowerContentType := strings.ToLower(contentType)
			if strings.HasPrefix(lowerContentType, "text/plain") || strings.HasPrefix(lowerContentType, "text/html") {
				log.Printf("Body:\n%s\n", string(bodyContent))
			} else if strings.HasPrefix(lowerContentType, "multipart/") {
				bodyLength := float64(len(bodyContent))
				contentPreviewLength := int(math.Min(bodyLength, 500))
				log.Printf("Multipart Body, can't parse directly (preview):\n%s\n", string(bodyContent[:contentPreviewLength]))
			} else {
				bodyLength := float64(len(bodyContent))
				contentPreviewLength := int(math.Min(bodyLength, 500))
				log.Printf("Other Body Content (Type: %s, preview):\n%s\n", contentType, string(bodyContent[:contentPreviewLength]))
			}
		} else {
			log.Println("Email body is empty or could not be read.")
		}
	}

	return nil
}

func main() {
	lambda.Start(handleRequest)
}
