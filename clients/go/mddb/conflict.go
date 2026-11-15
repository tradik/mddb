package mddb

import (
	"encoding/json"
	"net/http"
)

// RevisionConflict is a conditional write or delete that lost: the document
// is not at the revision the request expected. Expected is empty for a
// create that required the document to be absent; Current is empty when the
// document does not exist.
type RevisionConflict struct {
	Expected string `json:"expectedRevision"`
	Current  string `json:"currentRevision"`
}

// RevisionConflict reports the conflict a 409 answer describes, and false
// for any other error. The server answers every lost condition with the same
// body, so this works for Add and Delete alike:
//
//	var apiErr *mddb.APIError
//	if errors.As(err, &apiErr) {
//		if c, ok := apiErr.RevisionConflict(); ok {
//			reload(c.Current)
//		}
//	}
func (e *APIError) RevisionConflict() (*RevisionConflict, bool) {
	if e.StatusCode != http.StatusConflict {
		return nil, false
	}
	var body struct {
		Error string `json:"error"`
		RevisionConflict
	}
	if json.Unmarshal([]byte(e.Body), &body) != nil || body.Error != "revision conflict" {
		return nil, false
	}
	return &body.RevisionConflict, true
}
