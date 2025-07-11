package main

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"log"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/mail"
	"strings"
)

type EmailContent struct {
	PlainText string
	HTML      string
	To        string
	From      string
	Subject   string
}

// get RAW shit from an email
func ParseEmailBody(r io.Reader) (*EmailContent, error) {
	msg, err := mail.ReadMessage(r)
	if err != nil {
		return nil, fmt.Errorf("failed to read email message: %w", err)
	}

	contentTypeHeader := msg.Header.Get("Content-Type")
	mediaType, params, err := mime.ParseMediaType(contentTypeHeader)
	if err != nil {
		return nil, fmt.Errorf("failed to parse Content-Type header: %w", err)
	}

	emailContent := &EmailContent{}
	emailContent.To = msg.Header.Get("To")
	emailContent.From = msg.Header.Get("From")
	emailContent.Subject = msg.Header.Get("Subject")

	if strings.HasPrefix(mediaType, "multipart/") {
		mr := multipart.NewReader(msg.Body, params["boundary"])
		for {
			p, err := mr.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				return nil, fmt.Errorf("failed to read multipart part: %w", err)
			}

			partMediaType, partParams, err := mime.ParseMediaType(p.Header.Get("Content-Type"))
			if err != nil {
				log.Printf("Warning: Failed to parse part Content-Type: %v", err)
				continue
			}

			b, err := io.ReadAll(p)
			if err != nil {
				log.Printf("Warning: Failed to read part body: %v", err)
				continue
			}

			cte := p.Header.Get("Content-Transfer-Encoding")
			decodedBytes := b
			if cte != "" {
				switch strings.ToLower(cte) {
				case "base64":
					decodedBytes, err = io.ReadAll(base64.NewDecoder(base64.StdEncoding, bytes.NewReader(b)))
					if err != nil {
						log.Printf("Warning: Failed to base64 decode part: %v", err)
						continue
					}
				case "quoted-printable":
					reader := quotedprintable.NewReader(bytes.NewReader(b))
					decodedBytes, err = io.ReadAll(reader)
					if err != nil {
						return nil, fmt.Errorf("failed to quoted-printable decode: %w", err)
					}
				default:
					log.Printf("Warning: Unhandled Content-Transfer-Encoding: %s", cte)
				}
			}

			switch {
			case strings.HasPrefix(partMediaType, "text/plain"):
				charset := partParams["charset"]
				if charset == "" {
					charset = "utf-8"
				}
				emailContent.PlainText = string(decodedBytes)

			case strings.HasPrefix(partMediaType, "text/html"):
				charset := partParams["charset"]
				if charset == "" {
					charset = "utf-8"
				}
				emailContent.HTML = string(decodedBytes)

			case strings.HasPrefix(partMediaType, "application/"):
				log.Printf("Found attachment: %s, Filename: %s", partMediaType, p.FileName())

			default:
				log.Printf("Ignoring unsupported part type: %s, Filename: %s", partMediaType, p.FileName())
			}
		}
	} else {
		b, err := io.ReadAll(msg.Body)
		if err != nil {
			return nil, fmt.Errorf("failed to read single part email body: %w", err)
		}

		cte := msg.Header.Get("Content-Transfer-Encoding")
		decodedBytes := b
		if cte != "" {
			switch strings.ToLower(cte) {
			case "base64":
				decodedBytes, err = io.ReadAll(base64.NewDecoder(base64.StdEncoding, bytes.NewReader(b)))
				if err != nil {
					log.Printf("Warning: Failed to base64 decode single part: %v", err)
				}
			case "quoted-printable":
				reader := quotedprintable.NewReader(bytes.NewReader(b))
				decodedBytes, err = io.ReadAll(reader)
				if err != nil {
					return nil, fmt.Errorf("failed to quoted-printable decode: %w", err)
				}
			default:
				log.Printf("Warning: Unhandled Content-Transfer-Encoding for single part: %s", cte)
			}
		}

		switch {
		case strings.HasPrefix(mediaType, "text/plain"):
			emailContent.PlainText = string(decodedBytes)
		case strings.HasPrefix(mediaType, "text/html"):
			emailContent.HTML = string(decodedBytes)
		default:
			log.Printf("Warning: Single part email with unhandled type: %s", mediaType)
		}
	}

	return emailContent, nil
}
