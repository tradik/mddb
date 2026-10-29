package benchkit

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
)

// Post sends body as JSON and returns the response, or why it failed.
func Post(client *http.Client, url string, body any) ([]byte, error) {
	buf, _ := json.Marshal(body)
	resp, err := client.Post(url, "application/json", bytes.NewReader(buf)) // #nosec G107 -- the operator's own server
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("POST %s: %d %s", url, resp.StatusCode, raw)
	}
	return raw, nil
}

// Mean of xs; 0 for none.
func Mean(xs []float64) float64 {
	var s float64
	for _, x := range xs {
		s += x
	}
	return s / float64(max(len(xs), 1))
}

// Percentile p (0–100) of xs by nearest rank; 0 for none.
func Percentile(xs []float64, p int) float64 {
	if len(xs) == 0 {
		return 0
	}
	s := append([]float64(nil), xs...)
	sort.Float64s(s)
	return s[min(len(s)-1, len(s)*p/100)]
}
