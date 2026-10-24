package services

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"vestt/common/httpclient"
)

// OmdbResolver ports upstream OmdbResolver (Jackett.Common/Services/
// ImdbResolver.cs): resolves an IMDb id to a movie title via the OMDB API,
// backing the meta-indexer fallback strategies and the IMDb title result
// filter.
type OmdbResolver struct {
	client *httpclient.Client
	apiKey string
	apiURL string
}

// compile-time interface check (meta.ImdbResolver).
var _ interface {
	MovieTitle(ctx context.Context, imdbID string) (string, error)
} = (*OmdbResolver)(nil)

// NewOmdbResolver builds the resolver (apiURL empty selects the upstream
// default https://omdbapi.com).
func NewOmdbResolver(client *httpclient.Client, apiKey, apiURL string) *OmdbResolver {
	return &OmdbResolver{client: client, apiKey: apiKey, apiURL: apiURL}
}

// MovieTitle mirrors upstream MovieForId: GET {api}/?apikey=<key>&i=tt<id>
// and returns the Title field of the JSON response.
func (r *OmdbResolver) MovieTitle(ctx context.Context, id string) (string, error) {
	imdbID := id
	if !strings.HasPrefix(imdbID, "tt") {
		imdbID = "tt" + imdbID
	}
	base := r.apiURL
	if base == "" {
		base = "https://omdbapi.com"
	}
	u, err := url.Parse(base)
	if err != nil {
		return "", fmt.Errorf("omdb: bad api url: %w", err)
	}
	q := u.Query()
	q.Set("apikey", r.apiKey)
	q.Set("i", imdbID)
	u.RawQuery = q.Encode()

	res, err := r.client.Do(ctx, "", &httpclient.Request{Method: "GET", URL: u.String()})
	if err != nil {
		return "", err
	}
	var movie struct {
		Title string `json:"Title"`
	}
	if err := json.Unmarshal(res.Body, &movie); err != nil {
		return "", fmt.Errorf("omdb: decode response: %w", err)
	}
	return movie.Title, nil
}
