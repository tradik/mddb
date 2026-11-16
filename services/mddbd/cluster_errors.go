package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// What a node that is not the leader answers a write with (GO-051): the
// leader's address, so the client can send it there. REST: 421 with the
// X-MDDB-Leader header and leaderAddr in the body, or 503 while no leader is
// known; gRPC: FailedPrecondition (Unavailable) with an ErrorInfo detail;
// MCP: the error text names the address.

// notLeaderError is a write refused by a node that does not lead.
type notLeaderError struct {
	LeaderID   string
	LeaderHTTP string
	LeaderGRPC string
	Term       uint64
}

func (e *notLeaderError) known() bool { return e.LeaderID != "" }

func (e *notLeaderError) Error() string {
	if !e.known() {
		return fmt.Sprintf("no leader: an election is in progress (term %d)", e.Term)
	}
	return fmt.Sprintf("not the leader: send writes to %s (leader %s, term %d)", e.LeaderHTTP, e.LeaderID, e.Term)
}

// notLeader describes the leader this node knows.
func (c *clusterRuntime) notLeader() *notLeaderError {
	id, http, term := c.node.Leader()
	e := &notLeaderError{LeaderID: id, LeaderHTTP: http, Term: term}
	for _, p := range c.settings.node.Peers {
		if p.ID == id {
			e.LeaderGRPC = p.Addr
		}
	}
	return e
}

// writeError is why this node does not take a write now, or nil.
func (c *clusterRuntime) writeError() error {
	if c.node.CanWrite() != nil {
		return c.notLeader()
	}
	return nil
}

// writeNotLeader answers an HTTP write refused because this node does not
// lead.
func writeNotLeader(w http.ResponseWriter, e *notLeaderError) {
	body := map[string]any{"error": "no leader: an election is in progress", "term": e.Term}
	code := http.StatusServiceUnavailable
	if e.known() {
		body = map[string]any{"error": "not the leader", "leaderAddr": e.LeaderHTTP, "leaderId": e.LeaderID, "term": e.Term}
		w.Header().Set("X-MDDB-Leader", e.LeaderHTTP)
		code = http.StatusMisdirectedRequest
	} else {
		w.Header().Set("Retry-After", "1")
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(body)
}

// grpcStatus is the gRPC form, with the leader in an ErrorInfo detail.
func (e *notLeaderError) grpcStatus() error {
	code, reason := codes.Unavailable, "NO_LEADER"
	if e.known() {
		code, reason = codes.FailedPrecondition, "NOT_LEADER"
	}
	st := status.New(code, e.Error())
	info := &errdetails.ErrorInfo{Reason: reason, Domain: "mddb", Metadata: map[string]string{
		"leader_addr": e.LeaderHTTP, "leader_grpc": e.LeaderGRPC, "leader_id": e.LeaderID,
		"term": strconv.FormatUint(e.Term, 10),
	}}
	if withInfo, err := st.WithDetails(info); err == nil {
		st = withInfo
	}
	return st.Err()
}
