package httpclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// flaresolverrRequest mirrors the FlareSolverr v1 protocol payload
// (SPEC-04 §3.2 flaresolverr.go).
type flaresolverrRequest struct {
	Cmd        string `json:"cmd"`
	URL        string `json:"url"`
	MaxTimeout int    `json:"maxTimeout"`
}

type flaresolverrSolution struct {
	URL       string           `json:"url"`
	Status    int              `json:"status"`
	UserAgent string           `json:"userAgent"`
	Cookies   []solutionCookie `json:"cookies"`
	Response  string           `json:"response"`
}

type solutionCookie struct {
	Name     string  `json:"name"`
	Value    string  `json:"value"`
	Domain   string  `json:"domain"`
	Path     string  `json:"path"`
	Expires  float64 `json:"expires"`
	HttpOnly bool    `json:"httpOnly"`
	Secure   bool    `json:"secure"`
}

type flaresolverrResponse struct {
	Status   string                `json:"status"`
	Message  string                `json:"message"`
	Solution *flaresolverrSolution `json:"solution"`
}

// solveChallenge calls FlareSolverr and, on success, injects the returned
// cookies into the indexer jar and overrides the indexer User-Agent. It
// returns false when no FlareSolverr is configured.
func (c *Client) solveChallenge(ctx context.Context, indexerID string, st *indexerState, targetURL string) (bool, error) {
	if strings.TrimSpace(c.cfg.FlareSolverrURL) == "" {
		return false, nil
	}

	endpoint := strings.TrimRight(c.cfg.FlareSolverrURL, "/") + "/v1"
	payload, err := json.Marshal(flaresolverrRequest{
		Cmd:        "request.get",
		URL:        targetURL,
		MaxTimeout: c.cfg.FlareSolverrMaxTimeout,
	})
	if err != nil {
		return false, err
	}

	reqCtx, cancel := context.WithTimeout(ctx, time.Duration(c.cfg.FlareSolverrMaxTimeout)*time.Millisecond+30*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return false, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.hc.Do(req)
	if err != nil {
		return false, fmt.Errorf("FlareSolverr request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return false, err
	}

	var fsResp flaresolverrResponse
	if err := json.Unmarshal(body, &fsResp); err != nil {
		return false, fmt.Errorf("FlareSolverr invalid response: %w", err)
	}
	if fsResp.Status != "ok" || fsResp.Solution == nil {
		return false, &ErrFlareSolverrFailed{Message: fmt.Sprintf("FlareSolverr error: %s", fsResp.Message)}
	}

	// inject solution cookies into the per-indexer jar
	sol := fsResp.Solution
	if len(sol.Cookies) > 0 {
		u, _ := url.Parse(targetURL)
		cookies := make([]*http.Cookie, 0, len(sol.Cookies))
		for _, sc := range sol.Cookies {
			ck := &http.Cookie{
				Name:     sc.Name,
				Value:    sc.Value,
				Domain:   sc.Domain,
				Path:     sc.Path,
				HttpOnly: sc.HttpOnly,
				Secure:   sc.Secure,
			}
			if sc.Expires > 0 {
				ck.Expires = time.Unix(int64(sc.Expires), 0)
			}
			cookies = append(cookies, ck)
		}
		st.jar.setCookies(u, cookies)
		if c.saver != nil {
			c.saver(indexerID, st.jar.snapshot())
		}
	}
	if sol.UserAgent != "" {
		st.userAgent = sol.UserAgent
	}
	return true, nil
}
