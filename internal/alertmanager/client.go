package alertmanager

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type Client struct {
	baseURL string
	http    *http.Client
}

func New(client *http.Client, baseURL string) *Client {
	return &Client{http: client, baseURL: strings.TrimRight(baseURL, "/")}
}

type Matcher struct {
	Name    string `json:"name"`
	Value   string `json:"value"`
	IsRegex bool   `json:"isRegex"`
	IsEqual bool   `json:"isEqual"`
}
type Silence struct {
	ID        string    `json:"id,omitempty"`
	Matchers  []Matcher `json:"matchers"`
	StartsAt  time.Time `json:"startsAt"`
	EndsAt    time.Time `json:"endsAt"`
	CreatedBy string    `json:"createdBy"`
	Comment   string    `json:"comment"`
}

func (c *Client) CreateSilence(ctx context.Context, s Silence) (string, error) {
	body, _ := json.Marshal(s)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/v2/silences", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("Alertmanager returned %d: %s", resp.StatusCode, data)
	}
	var out struct {
		SilenceID string `json:"silenceID"`
	}
	if err = json.Unmarshal(data, &out); err != nil {
		return "", err
	}
	if out.SilenceID == "" {
		return "", fmt.Errorf("Alertmanager returned empty silenceID")
	}
	return out.SilenceID, nil
}
