package gh

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

const maxGraphQLResponse = 32 << 20

// GraphQLError is an error item from a GraphQL response.
type GraphQLError struct {
	Message string `json:"message"`
	Type    string `json:"type"`
	Path    []any  `json:"path"`
	Locs    []any  `json:"locations"`
}

// GraphQLErrors wraps the errors array.
type GraphQLErrors []GraphQLError

func (e GraphQLErrors) Error() string {
	msgs := make([]string, 0, len(e))
	for _, item := range e {
		msgs = append(msgs, item.Message)
	}
	return "graphql: " + strings.Join(msgs, "; ")
}

// graphql executes a query and decodes data into out.
func (c *Client) graphql(ctx context.Context, query string, vars map[string]any, out any) error {
	body, err := json.Marshal(map[string]any{"query": query, "variables": vars})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.graphqlURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("graphql request: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxGraphQLResponse))
	if err != nil {
		return fmt.Errorf("reading graphql response: %w", err)
	}
	if resp.StatusCode == http.StatusUnauthorized {
		return ErrReauthRequired
	}
	if resp.StatusCode != http.StatusOK {
		return &APIError{Status: resp.StatusCode, Message: strings.TrimSpace(string(raw))}
	}
	var env struct {
		Data   json.RawMessage `json:"data"`
		Errors GraphQLErrors   `json:"errors"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return fmt.Errorf("parsing graphql response: %w", err)
	}
	if len(env.Errors) > 0 {
		// Partial data can accompany errors (e.g. one search alias failed);
		// decode what we have and still surface the error.
		if len(env.Data) > 0 && out != nil {
			_ = json.Unmarshal(env.Data, out)
		}
		return env.Errors
	}
	if out != nil && len(env.Data) > 0 {
		if err := json.Unmarshal(env.Data, out); err != nil {
			return fmt.Errorf("decoding graphql data: %w", err)
		}
	}
	return nil
}

// APIError is a non-2xx REST or GraphQL transport response.
type APIError struct {
	Status  int
	Message string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("github api: %d %s", e.Status, e.Message)
}
