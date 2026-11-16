package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/spf13/cobra"
	mddb "github.com/tradik/mddb/clients/go/mddb/v2"
)

// mddb-cli placement move (GO-052): move a whole collection to another node
// group.

// placementWaitTimeout bounds `placement move --wait` when --timeout is not
// given: copying a large collection takes far longer than a normal call.
const placementWaitTimeout = time.Hour

// placementMoveReply is the body of POST /v1/placement/move, success or 409.
type placementMoveReply struct {
	Error string         `json:"error"`
	Move  *placementMove `json:"move"`
}

func newPlacementMoveCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "move <collection> <group>",
		Short: "Move a collection to another node group (admin)",
		Long: `Move a whole collection to another node group. The target group copies the
collection, catches up with the writes made meanwhile, and the collection is
fenced for a moment while the placement switches over. Without --wait the
command returns once the move has started; with --wait it returns when the
move ends and exits non-zero when the move failed.`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			wait, _ := cmd.Flags().GetBool("wait")
			timeout, _ := cmd.Flags().GetDuration("timeout")
			return runPlacementMove(args[0], args[1], wait, timeout)
		},
	}
	cmd.Flags().Bool("wait", false, "wait until the move ends and report its outcome")
	cmd.Flags().Duration("timeout", placementWaitTimeout, "how long --wait waits for the move")
	return cmd
}

// runPlacementMove posts the move and prints how it started or ended.
func runPlacementMove(collection, group string, wait bool, timeout time.Duration) error {
	path := "/v1/placement/move"
	ctx := context.Background()
	if wait {
		path += "?wait=true"
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	body := map[string]string{"collection": collection, "group": group}
	resp, err := newClient().Do(ctx, http.MethodPost, path, body)
	if err != nil {
		return failedMoveError(err)
	}
	if outputJSON {
		fmt.Println(string(resp))
		return nil
	}
	var r placementMoveReply
	if err := json.Unmarshal(resp, &r); err != nil {
		return fmt.Errorf("parse response: %w", err)
	}
	if r.Move == nil {
		return errors.New("parse response: no move in the answer")
	}
	printMoveResult(*r.Move, wait)
	return nil
}

// printMoveResult prints a started move, or a finished one with --wait.
func printMoveResult(m placementMove, wait bool) {
	if !wait {
		fmt.Printf("Moving %s from %s to %s: %s\n", m.Collection, m.From, m.To, m.Phase)
		return
	}
	fmt.Printf("Move of %s from %s to %s: %s, %d keys copied in %s\n",
		m.Collection, m.From, m.To, m.Phase, m.KeysCopied, moveOutcome(m))
}

// failedMoveError turns the 409 a failed --wait move answers with into an
// error carrying the server's text and the move's phase; any other error,
// a plain 409 included, is returned as it is.
func failedMoveError(err error) error {
	var apiErr *mddb.APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusConflict {
		return err
	}
	var r placementMoveReply
	if json.Unmarshal([]byte(apiErr.Body), &r) != nil || r.Move == nil || r.Error == "" {
		return err
	}
	if outputJSON {
		fmt.Println(apiErr.Body)
	}
	return fmt.Errorf("move of %s from %s to %s %s: %s", r.Move.Collection, r.Move.From, r.Move.To, r.Move.Phase, r.Error)
}
