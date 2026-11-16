package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"mddb/internal/cluster"
)

// The cluster on the HTTP API (GO-051): its part of /v1/replication/status,
// and promotion and demotion for an operator.

// fillStatus adds the cluster to a status response: the node's role is its
// election state, and a follower reports its stream from the leader.
func (c *clusterRuntime) fillStatus(resp *ReplicationStatusResponse) {
	st := c.node.Status()
	resp.Role, resp.Term = string(st.State), st.Term
	_, lastTerm := c.s.Binlog.LastPosition()
	cs := &ClusterStatusJSON{Status: st, AckMode: string(c.settings.ack), lastTerm: lastTerm}
	if f := c.rejected.Load(); f != nil {
		cs.LastRejectedFile = *f
	}
	resp.Cluster = cs
	if rc := c.client.Load(); rc != nil {
		rc.fillStatus(resp)
		resp.CurrentLSN = c.s.Binlog.CurrentLSN()
	}
	why := clusterUnhealthy(st)
	resp.Healthy = resp.Healthy && why == ""
	if resp.LastError == "" {
		resp.LastError = why
	}
}

// clusterUnhealthy says why a cluster node's status is unhealthy, or "": a
// node that leads but may not take writes — taking over, or without a
// majority's lease.
func clusterUnhealthy(st cluster.Status) string {
	if st.State == cluster.Leader && !st.Writable {
		return "this node leads but takes no writes: it is taking over, or no majority answered its lease"
	}
	return ""
}

// promoteRequest is the body of POST /v1/replication/promote.
type promoteRequest struct {
	Force bool `json:"force"`
}

// roleResponse is what promote and demote answer.
type roleResponse struct {
	NodeID string `json:"node_id"`
	State  string `json:"state"`
	Term   uint64 `json:"term"`
}

// promoteTimeout bounds a promotion: probing, the hand-over, the catch-up
// and the vote.
const promoteTimeout = 30 * time.Second

// handleReplicationPromote is POST /v1/replication/promote (admin): make
// this node the leader, if no reachable node has a later log.
func (s *Server) handleReplicationPromote(w http.ResponseWriter, r *http.Request) {
	c, ok := s.clusterAdmin(w, r)
	if !ok {
		return
	}
	var req promoteRequest
	if r.ContentLength != 0 {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSONStatus(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
			return
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), promoteTimeout)
	defer cancel()
	if err := c.node.Promote(ctx, cluster.PromoteOptions{Force: req.Force}); err != nil {
		writeJSONStatus(w, http.StatusConflict, map[string]string{"error": err.Error()})
		return
	}
	writeJSONStatus(w, http.StatusOK, c.role())
}

// handleReplicationDemote is POST /v1/replication/demote (admin): the
// leader stops taking writes and lets the others elect a successor.
func (s *Server) handleReplicationDemote(w http.ResponseWriter, r *http.Request) {
	c, ok := s.clusterAdmin(w, r)
	if !ok {
		return
	}
	if err := c.node.Demote(); err != nil {
		writeJSONStatus(w, http.StatusConflict, map[string]string{"error": err.Error()})
		return
	}
	writeJSONStatus(w, http.StatusOK, c.role())
}

// clusterAdmin admits a POST to a node in a cluster. Who may send it is the
// access gate's to decide: both routes are ruleAdmin (access_rules_http.go).
func (s *Server) clusterAdmin(w http.ResponseWriter, r *http.Request) (*clusterRuntime, bool) {
	if r.Method != http.MethodPost {
		writeJSONStatus(w, http.StatusMethodNotAllowed, map[string]string{"error": "use POST"})
		return nil, false
	}
	if s.cluster == nil {
		writeJSONStatus(w, http.StatusBadRequest, map[string]string{"error": errNotClustered.Error()})
		return nil, false
	}
	return s.cluster, true
}

var errNotClustered = errors.New("this node is not in a cluster: set MDDB_CLUSTER_PEERS")

func (c *clusterRuntime) role() roleResponse {
	return roleResponse{NodeID: c.s.NodeID, State: string(c.node.State()), Term: c.node.Term()}
}
