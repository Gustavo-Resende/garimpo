package evolution

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

type Client struct {
	baseURL      string
	apiKey       string
	instanceName string
	groupJID     string
	httpClient   *http.Client
}

func NewClient(baseURL, apiKey, instanceName, groupJID string) *Client {
	return &Client{
		baseURL:      baseURL,
		apiKey:       apiKey,
		instanceName: instanceName,
		groupJID:     groupJID,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

type sendTextBody struct {
	Number string `json:"number"`
	Text   string `json:"text"`
}

func (c *Client) SendMessage(text string) error {
	var lastErr error
	for range 3 {
		if err := c.doSend(text); err == nil {
			return nil
		} else {
			lastErr = err
		}
	}
	return lastErr
}

func (c *Client) doSend(text string) error {
	body := sendTextBody{
		Number: c.groupJID,
		Text:   text,
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("evolution: marshal body: %w", err)
	}

	url := fmt.Sprintf("%s/message/sendText/%s", c.baseURL, c.instanceName)
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("evolution: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("apikey", c.apiKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("evolution: http request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("evolution: read response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("evolution: status %d: %s", resp.StatusCode, respBody)
	}

	return nil
}
