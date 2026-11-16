package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// mddb-cli placement [status] (GO-052): the node groups, which group holds
// each collection, and the recent moves. The move subcommand is in
// cmd_placement_move.go.

// placementNode is one node of a group.
type placementNode struct {
	ID   string `json:"id"`
	HTTP string `json:"http"`
	GRPC string `json:"grpc"`
}

// placementGroup is one group of nodes (a cluster: leader and followers).
type placementGroup struct {
	Name        string          `json:"name"`
	Meta        bool            `json:"meta"`
	Leader      string          `json:"leader"`
	LeaderHTTP  string          `json:"leaderHttp"`
	Nodes       []placementNode `json:"nodes"`
	Collections int64           `json:"collections"`
}

// placementCollection is where one collection lives.
type placementCollection struct {
	Name     string `json:"name"`
	Group    string `json:"group"`
	MovingTo string `json:"movingTo"`
}

// placementMove is one collection move, running or finished.
type placementMove struct {
	Collection string `json:"collection"`
	From       string `json:"from"`
	To         string `json:"to"`
	Phase      string `json:"phase"`
	StartedAt  string `json:"startedAt"`
	FinishedAt string `json:"finishedAt"`
	KeysCopied int64  `json:"keysCopied"`
	SourceLSN  int64  `json:"sourceLsn"`
	AppliedLSN int64  `json:"appliedLsn"`
	Error      string `json:"error"`
	DurationMs int64  `json:"durationMs"`
}

// placementStatus is the body of GET /v1/placement.
type placementStatus struct {
	Enabled     bool                  `json:"enabled"`
	NodeID      string                `json:"nodeId"`
	Group       string                `json:"group"`
	MetaGroup   string                `json:"metaGroup"`
	Version     int64                 `json:"version"`
	Term        int64                 `json:"term"`
	Route       string                `json:"route"`
	Groups      []placementGroup      `json:"groups"`
	Collections []placementCollection `json:"collections"`
	Moves       []placementMove       `json:"moves"`
}

func newPlacementCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "placement",
		Short: "Collection placement across node groups",
		Long: `Show which node group holds each collection, and move a collection to
another group (servers with MDDB_PLACEMENT_GROUPS). Without a subcommand it
runs "placement status".`,
		Args: cobra.NoArgs,
		RunE: runPlacementStatus,
	}
	cmd.AddCommand(newPlacementStatusCmd(), newPlacementMoveCmd())
	return cmd
}

func newPlacementStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show the groups, where each collection lives and recent moves",
		Args:  cobra.NoArgs,
		RunE:  runPlacementStatus,
	}
}

// runPlacementStatus fetches GET /v1/placement and prints it.
func runPlacementStatus(cmd *cobra.Command, args []string) error {
	resp, err := newClient().Do(context.Background(), http.MethodGet, "/v1/placement", nil)
	if err != nil {
		return err
	}
	if outputJSON {
		fmt.Println(string(resp))
		return nil
	}
	var st placementStatus
	if err := json.Unmarshal(resp, &st); err != nil {
		return fmt.Errorf("parse response: %w", err)
	}
	printPlacementStatus(st)
	return nil
}

// printPlacementStatus prints the placement as a short report.
func printPlacementStatus(st placementStatus) {
	if !st.Enabled {
		fmt.Println("Placement is not configured on this server (MDDB_PLACEMENT_GROUPS).")
		return
	}
	fmt.Printf("Node:       %s\n", st.NodeID)
	fmt.Printf("Group:      %s\n", st.Group)
	fmt.Printf("Meta group: %s\n", st.MetaGroup)
	fmt.Printf("Version:    %d (term %d)\n", st.Version, st.Term)
	fmt.Printf("Route:      %s\n", st.Route)
	printPlacementGroups(st.Groups)
	printPlacementCollections(st.Collections)
	printPlacementMoves(st.Moves)
}

// printPlacementGroups prints one row per group; "*" marks the meta group.
func printPlacementGroups(groups []placementGroup) {
	if len(groups) == 0 {
		return
	}
	fmt.Printf("\n%-12s %-5s %-12s %11s  %s\n", "Group", "Meta", "Leader", "Collections", "Nodes")
	for _, g := range groups {
		meta := ""
		if g.Meta {
			meta = "*"
		}
		fmt.Printf("%-12s %-5s %-12s %11d  %s\n", g.Name, meta, orDash(g.Leader), g.Collections, placementNodeIDs(g.Nodes))
	}
}

// placementNodeIDs lists a group's node IDs, comma separated.
func placementNodeIDs(nodes []placementNode) string {
	ids := make([]string, 0, len(nodes))
	for _, n := range nodes {
		ids = append(ids, n.ID)
	}
	return orDash(strings.Join(ids, ","))
}

// printPlacementCollections prints which group holds each collection.
func printPlacementCollections(cols []placementCollection) {
	if len(cols) == 0 {
		return
	}
	fmt.Printf("\n%-24s %s\n", "Collection", "Group")
	for _, c := range cols {
		group := c.Group
		if c.MovingTo != "" {
			group += " → " + c.MovingTo
		}
		fmt.Printf("%-24s %s\n", c.Name, group)
	}
}

// printPlacementMoves prints the recent moves in the order the server lists
// them.
func printPlacementMoves(moves []placementMove) {
	if len(moves) == 0 {
		return
	}
	fmt.Printf("\n%-24s %-20s %-12s %10s %21s  %s\n", "Move", "From → To", "Phase", "Keys", "Applied/Source LSN", "Duration/Error")
	for _, m := range moves {
		fmt.Printf("%-24s %-20s %-12s %10d %21s  %s\n", m.Collection, m.From+" → "+m.To, m.Phase,
			m.KeysCopied, fmt.Sprintf("%d/%d", m.AppliedLSN, m.SourceLSN), moveOutcome(m))
	}
}

// moveOutcome is the move's error when it failed, otherwise its duration
// ("-" while it runs).
func moveOutcome(m placementMove) string {
	if m.Error != "" {
		return m.Error
	}
	if m.DurationMs > 0 {
		return (time.Duration(m.DurationMs) * time.Millisecond).String()
	}
	return "-"
}

// orDash returns s, or "-" when it is empty.
func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
