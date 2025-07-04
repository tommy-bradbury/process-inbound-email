package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"time"
)

const (
	BaseURI string = "https://api.openai.com"
	OpenAIBetaHeader string = "assistants=v2"
	SilenceErrors int = 1 << 0 // Suppress internal error logging
	RecallThreadID int = 1 << 1 // Attempt to recall an existing thread ID
)

type Assistant struct {
	silenceErrors bool
	runID         string
	openAIKey     string
	assistantID   string
	threadID      string 
	httpClient    *http.Client
}

type OpenAIErrorResponse struct {
	Error struct {
		Message string `json:"message"`
		Type    string `json:"type"`
		Param   string `json:"param"`
		Code    string `json:"code"`
	} `json:"error"`
}

type CreateThreadResponse struct {
	ID        string                 `json:"id"`
	Object    string                 `json:"object"`
	CreatedAt int64                  `json:"created_at"`
	Metadata  map[string]interface{} `json:"metadata"`
}

type AddMessagePayload struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type RunThreadPayload struct {
	AssistantID string `json:"assistant_id"`
}

type RunThreadResponse struct {
	ID          string                 `json:"id"`
	Object      string                 `json:"object"`
	CreatedAt   int64                  `json:"created_at"`
	AssistantID string                 `json:"assistant_id"`
	ThreadID    string                 `json:"thread_id"`
	Status      string                 `json:"status"`
	Metadata    map[string]interface{} `json:"metadata"`
}

type PollRunResponse struct {
	ID          string                 `json:"id"`
	Object      string                 `json:"object"`
	CreatedAt   int64                  `json:"created_at"`
	AssistantID string                 `json:"assistant_id"`
	ThreadID    string                 `json:"thread_id"`
	Status      string                 `json:"status"`
	Metadata    map[string]interface{} `json:"metadata"`
}

type ListMessagesResponse struct {
	Object  string    `json:"object"`
	Data    []Message `json:"data"`
	FirstID string    `json:"first_id"`
	LastID  string    `json:"last_id"`
	HasMore bool      `json:"has_more"`
}

type Message struct {
	ID        string                 `json:"id"`
	Object    string                 `json:"object"`
	CreatedAt int64                  `json:"created_at"`
	ThreadID  string                 `json:"thread_id"`
	Role      string                 `json:"role"`
	Content   []Content              `json:"content"`
	Metadata  map[string]interface{} `json:"metadata"`
}

type Content struct {
	Type string `json:"type"`
	Text Text   `json:"text"`
}

type Text struct {
	Value       string        `json:"value"`
	Annotations []interface{} `json:"annotations"`
}

// NewAssistant creates a new Assistant instance.
// If empty, a new thread will be initialized.
func NewAssistant(openAIKey, assistantID string, configOptions int, initialThreadID string) (*Assistant, error) {
	a := &Assistant{
		silenceErrors: (configOptions & SilenceErrors) != 0,
		openAIKey:     openAIKey,
		assistantID:   assistantID,
		httpClient:    &http.Client{Timeout: 30 * time.Second},
	}

	// If RecallThreadID is set and an initialThreadID is provided, use it.
	if (configOptions&RecallThreadID != 0) && initialThreadID != "" {
		a.threadID = initialThreadID
		return a, nil
	}

	// Otherwise, initialize a new thread.
	if err := a.initialiseThread(); err != nil {
		return nil, fmt.Errorf("failed to initialize thread: %w", err)
	}

	return a, nil
}

func (assistant *Assistant) GetThreadID() string {
	return assistant.threadID
}

func (assistant *Assistant) SetThreadID(threadID string) {
	assistant.threadID = threadID
}

func GetOpenAICredential() (string, error) {
	openAICredential := os.Getenv("OPEN_AI_CREDENTIAL")
	if openAICredential == "" {
		return "", fmt.Errorf("OPEN_AI_CREDENTIAL environment variable not set")
	}
	return openAICredential, nil
}

func (a *Assistant) ResetThread() error {
	return a.initialiseThread()
}

func (a *Assistant) SetAssistantID(assistantID string) {
	a.assistantID = assistantID
}

// initialiseThread creates a new thread with the OpenAI API and sets the Assistant's threadID.
func (a *Assistant) initialiseThread() error {
	url := fmt.Sprintf("%s/v1/threads", BaseURI)
	req, err := http.NewRequest("POST", url, nil)
	if err != nil {
		a.logError(fmt.Sprintf("Failed to create request for thread initialization: %v", err))
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+a.openAIKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("OpenAI-Beta", OpenAIBetaHeader)

	resp, err := a.httpClient.Do(req)
	if err != nil {
		a.logError(fmt.Sprintf("Error sending request to initialize thread: %v", err))
		return fmt.Errorf("error sending request: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		a.logError(fmt.Sprintf("Failed to read response body for thread initialization: %v", err))
		return fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		var apiErr OpenAIErrorResponse
		if err := json.Unmarshal(bodyBytes, &apiErr); err != nil {
			a.logError(fmt.Sprintf("Non-OK status %d, but failed to unmarshal error response for thread init: %s", resp.StatusCode, string(bodyBytes)))
			return fmt.Errorf("thread initialization failed with status %d: %s", resp.StatusCode, string(bodyBytes))
		}
		a.logError(fmt.Sprintf("Thread initialization failed with status %d, error: %s", resp.StatusCode, apiErr.Error.Message))
		return fmt.Errorf("thread could not be initialised: %s", apiErr.Error.Message)
	}

	var threadResp CreateThreadResponse
	if err := json.Unmarshal(bodyBytes, &threadResp); err != nil {
		a.logError(fmt.Sprintf("Failed to unmarshal thread initialization response: %v, body: %s", err, string(bodyBytes)))
		return fmt.Errorf("failed to unmarshal thread response: %w", err)
	}

	a.threadID = threadResp.ID
	return nil
}

// AddMessageToThread adds a message to the current thread, runs the thread, and polls for a response.
// It returns the assistant's reply or an error if any step fails.
func (a *Assistant) AddMessageToThread(prompt string) (string, error) {
	if a.threadID == "" {
		return "", fmt.Errorf("thread not initialized. Call NewAssistant or ResetThread first")
	}

	payload := AddMessagePayload{
		Role:    "user",
		Content: prompt,
	}
	jsonPayload, err := json.Marshal(payload)
	if err != nil {
		a.logError(fmt.Sprintf("Failed to marshal message payload: %v", err))
		return "", fmt.Errorf("failed to marshal message payload: %w", err)
	}

	url := fmt.Sprintf("%s/v1/threads/%s/messages", BaseURI, a.threadID)
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonPayload))
	if err != nil {
		a.logError(fmt.Sprintf("Failed to create request for adding message: %v", err))
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+a.openAIKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("OpenAI-Beta", OpenAIBetaHeader)

	resp, err := a.httpClient.Do(req)
	if err != nil {
		a.logError(fmt.Sprintf("Error sending request to add message to thread: %v", err))
		return "", fmt.Errorf("error sending request: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		a.logError(fmt.Sprintf("Failed to read response body for adding message: %v", err))
		return "", fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		var apiErr OpenAIErrorResponse
		if err := json.Unmarshal(bodyBytes, &apiErr); err != nil {
			a.logError(fmt.Sprintf("Non-OK status %d, but failed to unmarshal error response for add message: %s", resp.StatusCode, string(bodyBytes)))
			return "", fmt.Errorf("add message failed with status %d: %s", resp.StatusCode, string(bodyBytes))
		}
		a.logError(fmt.Sprintf("Error when attempting to publish message to OpenAI thread, error: %s", apiErr.Error.Message))
		return "", fmt.Errorf("failed to add message: %s", apiErr.Error.Message)
	}

	// Run the thread after adding the message
	threadRunning := a.runThread()
	if !threadRunning {
		return "", fmt.Errorf("failed to run thread after adding message")
	}

	// Poll for the assistant's reply
	response := a.pollThreadForReply(3, 4) // Default retries and wait time
	if response == "" {
		return "", fmt.Errorf("failed to get reply from thread after polling")
	}
	return response, nil
}

// runThread initiates a run on the current OpenAI thread and sets the Assistant's runID.
// It returns true on success, false on failure.
func (a *Assistant) runThread() bool {
	payload := RunThreadPayload{
		AssistantID: a.assistantID,
	}
	jsonPayload, err := json.Marshal(payload)
	if err != nil {
		a.logError(fmt.Sprintf("Failed to marshal run thread payload: %v", err))
		return false
	}

	url := fmt.Sprintf("%s/v1/threads/%s/runs", BaseURI, a.threadID)
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonPayload))
	if err != nil {
		a.logError(fmt.Sprintf("Failed to create request for running thread: %v", err))
		return false
	}

	req.Header.Set("Authorization", "Bearer "+a.openAIKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("OpenAI-Beta", OpenAIBetaHeader)

	resp, err := a.httpClient.Do(req)
	if err != nil {
		a.logError(fmt.Sprintf("Error sending request to run thread: %v", err))
		return false
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		a.logError(fmt.Sprintf("Failed to read response body for running thread: %v", err))
		return false
	}

	if resp.StatusCode != http.StatusOK {
		var apiErr OpenAIErrorResponse
		if err := json.Unmarshal(bodyBytes, &apiErr); err != nil {
			a.logError(fmt.Sprintf("Non-OK status %d, but failed to unmarshal error response for run thread: %s", resp.StatusCode, string(bodyBytes)))
			return false
		}
		a.logError(fmt.Sprintf("Error when attempting to run OpenAI assistant on thread, error: %s", apiErr.Error.Message))
		return false
	}

	var runResp RunThreadResponse
	if err := json.Unmarshal(bodyBytes, &runResp); err != nil {
		a.logError(fmt.Sprintf("Failed to unmarshal run thread response: %v, body: %s", err, string(bodyBytes)))
		return false
	}

	a.runID = runResp.ID
	return true
}

// pollThreadForReply checks the run status periodically until it's completed or failed.
// It returns the assistant's last message on success, or an empty string on failure/timeout.
func (a *Assistant) pollThreadForReply(retries int, retryWait int) string {
	for i := 0; i <= retries; i++ {
		url := fmt.Sprintf("%s/v1/threads/%s/runs/%s", BaseURI, a.threadID, a.runID)
		req, err := http.NewRequest("GET", url, nil)
		if err != nil {
			a.logError(fmt.Sprintf("Failed to create request for polling run status: %v", err))
			return ""
		}

		req.Header.Set("Authorization", "Bearer "+a.openAIKey)
		req.Header.Set("OpenAI-Beta", OpenAIBetaHeader)

		resp, err := a.httpClient.Do(req)
		if err != nil {
			if i == retries { // Log error only on the last retry
				a.logError(fmt.Sprintf("Error sending request to poll run status on last retry: %v", err))
				return ""
			}
			time.Sleep(time.Duration(retryWait) * time.Second)
			continue
		}
		defer resp.Body.Close()

		bodyBytes, err := io.ReadAll(resp.Body)
		if err != nil {
			a.logError(fmt.Sprintf("Failed to read response body for polling run status: %v", err))
			time.Sleep(time.Duration(retryWait) * time.Second)
			continue
		}

		if resp.StatusCode != http.StatusOK {
			var apiErr OpenAIErrorResponse
			if err := json.Unmarshal(bodyBytes, &apiErr); err != nil {
				a.logError(fmt.Sprintf("Non-OK status %d, but failed to unmarshal error response for poll run: %s", resp.StatusCode, string(bodyBytes)))
			} else {
				a.logError(fmt.Sprintf("Poll thread failed with status %d, error: %s", resp.StatusCode, apiErr.Error.Message))
			}
			time.Sleep(time.Duration(retryWait) * time.Second)
			continue
		}

		var pollResp PollRunResponse
		if err := json.Unmarshal(bodyBytes, &pollResp); err != nil {
			a.logError(fmt.Sprintf("Failed to unmarshal poll run response: %v, body: %s", err, string(bodyBytes)))
			time.Sleep(time.Duration(retryWait) * time.Second)
			continue
		}

		switch pollResp.Status {
		case "failed":
			a.logError(fmt.Sprintf("OpenAI run failed with status: %s, details: %s", pollResp.Status, string(bodyBytes)))
			return ""
		case "completed":
			return a.GetLastMessage()
		default:
			time.Sleep(time.Duration(retryWait) * time.Second)
		}
	}
	a.logError("Polling for thread reply timed out after maximum retries.")
	return ""
}

// GetLastMessage retrieves the most recent message in the current thread.
// It returns the text content of the message or an empty string on failure.
func (a *Assistant) GetLastMessage() string {
	url := fmt.Sprintf("%s/v1/threads/%s/messages", BaseURI, a.threadID)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		a.logError(fmt.Sprintf("Failed to create request for getting last message: %v", err))
		return ""
	}

	req.Header.Set("Authorization", "Bearer "+a.openAIKey)
	req.Header.Set("OpenAI-Beta", OpenAIBetaHeader)

	resp, err := a.httpClient.Do(req)
	if err != nil {
		a.logError(fmt.Sprintf("Error sending request to get last message: %v", err))
		return ""
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		a.logError(fmt.Sprintf("Failed to read response body for getting last message: %v", err))
		return ""
	}

	if resp.StatusCode != http.StatusOK {
		var apiErr OpenAIErrorResponse
		if err := json.Unmarshal(bodyBytes, &apiErr); err != nil {
			a.logError(fmt.Sprintf("Non-OK status %d, but failed to unmarshal error response for get last message: %s", resp.StatusCode, string(bodyBytes)))
		} else {
			a.logError(fmt.Sprintf("Get last message failed with status %d, error: %s", resp.StatusCode, apiErr.Error.Message))
		}
		return ""
	}

	var messagesResp ListMessagesResponse
	if err := json.Unmarshal(bodyBytes, &messagesResp); err != nil {
		a.logError(fmt.Sprintf("Failed to unmarshal messages response: %v, body: %s", err, string(bodyBytes)))
		return ""
	}

	// Check if there are messages and if the first message has text content
	if len(messagesResp.Data) > 0 && len(messagesResp.Data[0].Content) > 0 && messagesResp.Data[0].Content[0].Type == "text" {
		return messagesResp.Data[0].Content[0].Text.Value
	}

	a.logError("No text content found in the last message or message structure is unexpected.")
	return ""
}

// logError is a wrapper function for logging errors, respecting the silenceErrors flag.
func (a *Assistant) logError(message string) {
	if !a.silenceErrors {
		log.Printf("Assistant Error: %s", message)
	}
}
