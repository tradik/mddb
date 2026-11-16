package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"mddb/internal/cluster"
)

// Placement settings read from the environment (GO-052). See docs/CLUSTER.md
// and docs/config.md. Without MDDB_PLACEMENT_GROUPS none of this applies.

// placementNode is one node of a group, as the configuration names it. The
// addresses are the only ones a node forwards requests to: a request never
// chooses where it is sent.
type placementNode struct {
	ID   string `json:"id"`
	GRPC string `json:"grpc"` // its gRPC address, MDDB_CLUSTER_PEERS' address
	HTTP string `json:"http"` // its REST base URL
}

// placementFile is the JSON of MDDB_PLACEMENT_GROUPS.
type placementFile struct {
	Meta   string                     `json:"meta"`
	Groups map[string][]placementNode `json:"groups"`
}

// placementConfig is a node's view of every group.
type placementConfig struct {
	meta         string
	groups       map[string][]placementNode
	names        []string // the group names, sorted
	self         string   // this node's group
	redirect     bool     // answer with the address instead of forwarding
	refresh      time.Duration
	dropGrace    time.Duration
	fenceTimeout time.Duration
}

// Defaults for the timings.
const (
	defaultPlacementRefresh = time.Second
	defaultDropGrace        = 30 * time.Second
	defaultFenceTimeout     = 10 * time.Second
)

// placementConfigFromEnv reads the groups; ok is false when the node is not
// in a placement.
func placementConfigFromEnv() (pc placementConfig, ok bool, err error) {
	raw := strings.TrimSpace(env("MDDB_PLACEMENT_GROUPS", ""))
	if raw == "" {
		return pc, false, nil
	}
	if path, isFile := strings.CutPrefix(raw, "@"); isFile {
		data, rerr := os.ReadFile(filepath.Clean(path))
		if rerr != nil {
			return pc, true, fmt.Errorf("MDDB_PLACEMENT_GROUPS: %w", rerr)
		}
		raw = string(data)
	}
	var f placementFile
	if err = json.Unmarshal([]byte(raw), &f); err != nil {
		return pc, true, fmt.Errorf("MDDB_PLACEMENT_GROUPS: %w", err)
	}
	pc = placementConfig{meta: f.Meta, groups: f.Groups,
		refresh: defaultPlacementRefresh, dropGrace: defaultDropGrace, fenceTimeout: defaultFenceTimeout}
	switch route := env("MDDB_PLACEMENT_ROUTE", "forward"); route {
	case "forward", "redirect":
		pc.redirect = route == "redirect"
	default:
		return pc, true, fmt.Errorf("MDDB_PLACEMENT_ROUTE=%q: use forward or redirect", route)
	}
	for _, d := range []struct {
		name string
		dst  *time.Duration
	}{
		{"MDDB_PLACEMENT_REFRESH", &pc.refresh},
		{"MDDB_PLACEMENT_DROP_GRACE", &pc.dropGrace},
		{"MDDB_PLACEMENT_FENCE_TIMEOUT", &pc.fenceTimeout},
	} {
		v, derr := envDuration(d.name)
		if derr != nil {
			return pc, true, derr
		}
		if v > 0 {
			*d.dst = v
		}
	}
	return pc, true, nil
}

// validate checks the groups and finds this node's: nodeID must be in
// exactly one, whose nodes are the cluster's peers.
func (pc *placementConfig) validate(nodeID string, peers []cluster.Peer) error {
	if len(pc.groups) == 0 {
		return errors.New("MDDB_PLACEMENT_GROUPS names no group")
	}
	if _, ok := pc.groups[pc.meta]; !ok {
		return fmt.Errorf("MDDB_PLACEMENT_GROUPS: the meta group %q is not among the groups", pc.meta)
	}
	pc.names = make([]string, 0, len(pc.groups))
	seen := map[string]string{}
	for name, nodes := range pc.groups {
		pc.names = append(pc.names, name)
		if err := checkGroupNodes(name, nodes, seen); err != nil {
			return err
		}
	}
	sort.Strings(pc.names)
	pc.self = seen[nodeID]
	if pc.self == "" {
		return fmt.Errorf("MDDB_PLACEMENT_GROUPS: this node (%q) is in no group", nodeID)
	}
	return samePeers(pc.self, pc.groups[pc.self], peers)
}

// checkGroupNodes checks one group's nodes and records them in seen.
func checkGroupNodes(name string, nodes []placementNode, seen map[string]string) error {
	if name == "" || len(nodes) == 0 {
		return fmt.Errorf("MDDB_PLACEMENT_GROUPS: group %q needs a name and nodes", name)
	}
	for _, n := range nodes {
		if n.ID == "" || n.GRPC == "" || !httpBaseURL(n.HTTP) {
			return fmt.Errorf("MDDB_PLACEMENT_GROUPS: node %q of group %q needs an id, a grpc address and an http(s) URL", n.ID, name)
		}
		if other, dup := seen[n.ID]; dup {
			return fmt.Errorf("MDDB_PLACEMENT_GROUPS: node %q is in groups %q and %q", n.ID, other, name)
		}
		seen[n.ID] = name
	}
	return nil
}

// httpBaseURL reports whether s is an absolute http or https URL.
func httpBaseURL(s string) bool {
	u, err := url.Parse(s)
	return err == nil && u.Host != "" && (u.Scheme == "http" || u.Scheme == "https")
}

// samePeers checks that a group's nodes are the cluster's peers: the group
// is the cluster, and two descriptions of it must not drift apart.
func samePeers(group string, nodes []placementNode, peers []cluster.Peer) error {
	want := map[string]string{}
	for _, n := range nodes {
		want[n.ID] = n.GRPC
	}
	if len(peers) != len(want) {
		return fmt.Errorf("group %q has %d nodes in MDDB_PLACEMENT_GROUPS and %d in MDDB_CLUSTER_PEERS; they must be the same nodes", group, len(want), len(peers))
	}
	for _, p := range peers {
		if want[p.ID] != p.Addr {
			return fmt.Errorf("node %q of group %q: MDDB_CLUSTER_PEERS says %q, MDDB_PLACEMENT_GROUPS %q", p.ID, group, p.Addr, want[p.ID])
		}
	}
	return nil
}

// node is the node called id and its group.
func (pc *placementConfig) node(id string) (placementNode, string, bool) {
	for _, g := range pc.names {
		for _, n := range pc.groups[g] {
			if n.ID == id {
				return n, g, true
			}
		}
	}
	return placementNode{}, "", false
}
