package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

type Client struct {
	baseURL string
	key     string
	http    *http.Client
}

func NewClient(baseURL, key string) *Client {
	return &Client{baseURL: strings.TrimRight(baseURL, "/"), key: key, http: http.DefaultClient}
}

func (c *Client) Do(ctx context.Context, method, path string, input any, output any) (int, error) {
	var body io.Reader
	if input != nil {
		encoded, err := json.Marshal(input)
		if err != nil {
			return 0, err
		}
		body = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+"/api/public/v1/launch"+path, body)
	if err != nil {
		return 0, err
	}
	req.Header.Set("authorization", "Bearer "+c.key)
	req.Header.Set("content-type", "application/json")
	res, err := c.http.Do(req)
	if err != nil {
		return 0, err
	}
	defer res.Body.Close()
	data, err := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	if err != nil {
		return res.StatusCode, err
	}
	if res.StatusCode >= 400 {
		return res.StatusCode, fmt.Errorf("e-volv API %s: %s", res.Status, strings.TrimSpace(string(data)))
	}
	if output != nil && len(data) > 0 {
		if err := json.Unmarshal(data, output); err != nil {
			return res.StatusCode, err
		}
	}
	return res.StatusCode, nil
}
