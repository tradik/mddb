package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"
)

// mddb-cli replication status|promote|demote (GO-051): the node's place in
// its cluster, and moving the leadership on purpose.

func newReplicationCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "replication",
		Short: "Replication and cluster leadership",
		Long:  "Show the node's replication and cluster state, promote it to leader or step it down (cluster nodes, MDDB_CLUSTER_PEERS).",
	}
	cmd.AddCommand(newReplicationStatusCmd(), newReplicationPromoteCmd(), newReplicationDemoteCmd())
	return cmd
}

func newReplicationStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show the node's role, term, leader and nodes",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			resp, err := newClient().Do(context.Background(), "GET", "/v1/replication/status", nil)
			if err != nil {
				return err
			}
			if outputJSON {
				fmt.Println(string(resp))
				return nil
			}
			var st map[string]interface{}
			if err := json.Unmarshal(resp, &st); err != nil {
				return fmt.Errorf("parse response: %w", err)
			}
			printReplicationStatus(st)
			return nil
		},
	}
}

// printReplicationStatus prints the status as a short report.
func printReplicationStatus(st map[string]interface{}) {
	fmt.Printf("Node:     %s\n", asString(st["node_id"]))
	fmt.Printf("Role:     %s\n", asString(st["role"]))
	fmt.Printf("LSN:      %d\n", int64(asFloat(st["current_lsn"])))
	c := asMap(st["cluster"])
	if c == nil {
		return
	}
	fmt.Printf("Term:     %d\n", int64(asFloat(c["term"])))
	fmt.Printf("Leader:   %s %s\n", asString(c["leader_id"]), asString(c["leader_addr"]))
	fmt.Printf("Writable: %v\n", c["writable"] == true)
	if f := asString(c["last_rejected_file"]); f != "" {
		fmt.Printf("Rejected writes: %s\n", f)
	}
	nodes, _ := c["nodes"].([]interface{})
	if len(nodes) == 0 {
		return
	}
	fmt.Printf("\n%-12s %-10s %6s %12s %10s\n", "Node", "State", "Term", "Last LSN", "Reachable")
	for _, raw := range nodes {
		n := asMap(raw)
		fmt.Printf("%-12s %-10s %6d %12d %10v\n", asString(n["id"]), asString(n["state"]),
			int64(asFloat(n["term"])), int64(asFloat(n["last_lsn"])), n["reachable"] == true)
	}
}

func newReplicationPromoteCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "promote",
		Short: "Make the node the cluster's leader (admin)",
		Long: `Make the node --server points at the leader. It is refused when another
reachable node has a later log. A reachable leader is asked to step down
first. --force takes the leadership without votes; it is only allowed with
automatic failover off, when the other nodes are known to be down.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			force, _ := cmd.Flags().GetBool("force")
			return postRole("/v1/replication/promote", map[string]interface{}{"force": force})
		},
	}
	cmd.Flags().Bool("force", false, "take the leadership without a majority (automatic failover off only)")
	return cmd
}

func newReplicationDemoteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "demote",
		Short: "Step the leader down (admin)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return postRole("/v1/replication/demote", nil)
		},
	}
}

// postRole sends a promotion or demotion and prints the node's new role.
func postRole(path string, body interface{}) error {
	resp, err := newClient().Do(context.Background(), "POST", path, body)
	if err != nil {
		return err
	}
	if outputJSON {
		fmt.Println(string(resp))
		return nil
	}
	var r map[string]interface{}
	if err := json.Unmarshal(resp, &r); err != nil {
		return fmt.Errorf("parse response: %w", err)
	}
	fmt.Printf("%s is %s in term %d\n", asString(r["node_id"]), asString(r["state"]), int64(asFloat(r["term"])))
	return nil
}
