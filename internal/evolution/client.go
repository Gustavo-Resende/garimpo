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

type sendMediaBody struct {
	Number    string `json:"number"`
	MediaType string `json:"mediatype"`
	MimeType  string `json:"mimetype"`
	Media     string `json:"media"`
	Caption   string `json:"caption"`
}

func (c *Client) SendMessage(imageURL, text string) error {
	var lastErr error
	for range 3 {
		if err := c.doSend(imageURL, text); err == nil {
			return nil
		} else {
			lastErr = err
		}
	}
	return lastErr
}

func (c *Client) doSend(imageURL, text string) error {
	body := sendMediaBody{
		Number:    c.groupJID,
		MediaType: "image",
		MimeType:  "image/jpeg",
		Media:     imageURL,
		Caption:   text,
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("evolution: marshal body: %w", err)
	}

	url := fmt.Sprintf("%s/message/sendMedia/%s", c.baseURL, c.instanceName)
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
