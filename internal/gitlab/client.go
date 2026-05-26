package gitlab

import (
	"fmt"

	"gitlab.com/gitlab-org/api/client-go"
	"golang.org/x/time/rate"
)

type Client struct {
	*gitlab.Client
	Limiter *rate.Limiter
}

func NewClient(host, token string, limit float64) (*Client, error) {
	if host == "" {
		return nil, fmt.Errorf("gitlab host is required")
	}
	if token == "" {
		return nil, fmt.Errorf("gitlab token is required")
	}

	git, err := gitlab.NewClient(token, gitlab.WithBaseURL(host))
	if err != nil {
		return nil, fmt.Errorf("failed to create gitlab client: %w", err)
	}

	// Initialize limiter with the provided limit (req/sec) and a burst of 1
	limiter := rate.NewLimiter(rate.Limit(limit), 1)

	return &Client{git, limiter}, nil
}
