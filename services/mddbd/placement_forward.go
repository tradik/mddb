package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strconv"
)

// Sending a REST request to another node (GO-052). Only to the nodes the
// configuration names: the request decides which group, never which
// address. With MDDB_PLACEMENT_ROUTE=redirect the client is told the
// address instead, the way a follower answers a write (421 and
// X-MDDB-Leader), with the group in X-MDDB-Group.

// maxPlacementHops bounds how often a request is sent on: twice covers a
// node whose map is a little behind sending it to the old group.
const maxPlacementHops = 2

// hopByHop are the headers that belong to one connection, not the request.
var hopByHop = []string{"Connection", "Keep-Alive", "Proxy-Authenticate", "Proxy-Authorization",
	"Te", "Trailer", "Transfer-Encoding", "Upgrade"}

// forward sends r to the leader of group and copies the answer back.
func (p *placementRuntime) forward(w http.ResponseWriter, r *http.Request, group string) {
	hops, _ := strconv.Atoi(r.Header.Get(hdrPlacementHops))
	if hops >= maxPlacementHops {
		writePlacementBusy(w, "the request went round the node groups; the placement is changing")
		return
	}
	if p.cfg.redirect {
		p.redirect(w, group)
		return
	}
	body, err := readBody(r)
	if err != nil {
		writeBodyError(w, err)
		return
	}
	for attempt := 0; attempt < 2; attempt++ {
		n, ok := p.leaderNode(group)
		if !ok {
			writePlacementBusy(w, errNoGroupLeader.Error())
			return
		}
		resp, err := p.send(r, n.HTTP, body, hops+1)
		if err != nil {
			p.leaders.forget(group) // ask again who leads
			if notSent(err) {
				continue // the request never left: try once more
			}
			writePlacementBusy(w, "the node group holding the collection did not answer: "+err.Error())
			return
		}
		if resp.StatusCode == http.StatusMisdirectedRequest && attempt == 0 {
			_ = resp.Body.Close()
			p.leaders.forget(group) // the leader changed under the request
			continue
		}
		copyResponse(w, resp)
		return
	}
	writePlacementBusy(w, "the node group holding the collection did not answer")
}

// send makes r's request to base with body.
func (p *placementRuntime) send(r *http.Request, base string, body []byte, hops int) (*http.Response, error) {
	// #nosec G704 -- base is a configured node's URL (MDDB_PLACEMENT_GROUPS), never taken from the request
	out, err := http.NewRequestWithContext(r.Context(), r.Method, base+r.URL.RequestURI(), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	out.Header = r.Header.Clone()
	for _, h := range hopByHop {
		out.Header.Del(h)
	}
	out.Header.Set(hdrPlacementHops, strconv.Itoa(hops))
	return p.client.Do(out) // #nosec G704 -- a configured node, as above
}

// notSent reports an error from before the request was sent — no
// connection — after which sending it again cannot repeat a write.
func notSent(err error) bool {
	var op *net.OpError
	return errors.As(err, &op) && op.Op == "dial"
}

// copyResponse writes resp to w and closes it.
func copyResponse(w http.ResponseWriter, resp *http.Response) {
	defer func() { _ = resp.Body.Close() }()
	for k, vs := range resp.Header {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	for _, h := range hopByHop {
		w.Header().Del(h)
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

// readBody reads r's body (already capped by withMaxBody) and puts it back.
func readBody(r *http.Request) ([]byte, error) {
	if r.Body == nil || r.Body == http.NoBody {
		return nil, nil
	}
	raw, err := io.ReadAll(r.Body)
	_ = r.Body.Close()
	r.Body = io.NopCloser(bytes.NewReader(raw))
	return raw, err
}

// redirect tells the client where group's leader is.
func (p *placementRuntime) redirect(w http.ResponseWriter, group string) {
	n, ok := p.leaderNode(group)
	if !ok {
		writePlacementBusy(w, errNoGroupLeader.Error())
		return
	}
	w.Header().Set("X-MDDB-Leader", n.HTTP)
	w.Header().Set(hdrPlacementGroup, group)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusMisdirectedRequest)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": "the collection is held by another node group",
		"leaderAddr": n.HTTP, "leaderId": n.ID, "group": group})
}
