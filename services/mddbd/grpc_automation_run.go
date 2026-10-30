package main

import (
	"context"
	"errors"

	proto "mddb/proto"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// RunAutomation implements the RunAutomation RPC (#377): a cron rule's sync
// or backup runs now, through the code its schedule fires (RunNow).
func (g *GRPCServer) RunAutomation(ctx context.Context, req *proto.RunAutomationRequest) (*proto.RunAutomationResponse, error) {
	if err := g.permit(ctx, "*", PermAdmin); err != nil {
		return nil, err
	}
	if g.server.AutomationManager == nil {
		return nil, status.Error(codes.FailedPrecondition, automationNotInitialized)
	}
	if req.GetId() == "" {
		return nil, status.Error(codes.InvalidArgument, "missing id")
	}
	g.server.Metrics.IncOp("automation_run", req.GetId())
	run, err := g.server.runAutomationNow(req.GetId())
	if err != nil {
		return nil, status.Error(automationRunCode(err), err.Error())
	}
	return &proto.RunAutomationResponse{
		RuleId:    run.RuleID,
		Action:    run.Action,
		Status:    run.Status,
		JobId:     run.JobID,
		StartedAt: run.StartedAt,
	}, nil
}

// automationRunCode is the gRPC code of a RunNow error.
func automationRunCode(err error) codes.Code {
	switch {
	case errors.Is(err, errRuleNotFound):
		return codes.NotFound
	case errors.Is(err, errActionRunning):
		return codes.Aborted
	case errors.Is(err, errSchedulerOff):
		return codes.Unavailable
	}
	return codes.InvalidArgument
}

// automationRunResultToProto converts a rule's last result for the wire.
func automationRunResultToProto(r *AutomationRunResult) *proto.AutomationRunResultProto {
	if r == nil {
		return nil
	}
	return &proto.AutomationRunResultProto{
		At:         r.At,
		Status:     r.Status,
		DurationMs: r.DurationMs,
		Error:      r.Error,
		Added:      safeInt32(r.Added),
		Updated:    safeInt32(r.Updated),
		Unchanged:  safeInt32(r.Unchanged),
		Deleted:    safeInt32(r.Deleted),
		Failed:     safeInt32(r.Failed),
	}
}
