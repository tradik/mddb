package main

import (
	"context"
	"fmt"
	"os"
)

// The evaluation stages of a run. The operator's evaluate command gets the
// validation part (MDDB_EVAL_DATASET), the model to score (MDDB_EVAL_MODEL:
// empty for the base model, the result file for the tuned one) and the path
// of the report to write (MDDB_EVAL_OUTPUT), inside the job directory. The
// report is read back through the job's os.Root as a regular file only.

// planEvaluation decides whether a run is evaluated, recording why not.
// Asked or not, a profile with an evaluate block evaluates a pairs dataset
// with a validation part unless the run said evaluate: false.
func (m *trainingManager) planEvaluation(j *trainingJob, p *trainerProfile) bool {
	reason := ""
	switch {
	case j.Evaluate != nil && !*j.Evaluate:
		return false
	case p.Evaluate == nil:
		reason = "the trainer profile has no evaluate command"
	case j.Format != datasetPairs:
		reason = "only a pairs dataset has expected outputs to score"
	case j.ValidationRecords == 0:
		reason = "the dataset has no validation part (validationRatio 0)"
	}
	m.update(j, func() {
		j.Evaluation = &trainingEvaluation{Skipped: reason}
	})
	return reason == ""
}

// evaluateStage runs the evaluate command once and stores its outcome. Only
// a cancel or a shutdown is returned as an error: a failed evaluation is a
// warning of the run.
func (m *trainingManager) evaluateStage(ctx context.Context, plan *runPlan, jr *os.Root, log *trainingLog, stage string) error {
	j, spec := plan.job, &plan.profile.Evaluate.commandSpec
	m.stage(j, log, stage)
	name, model := "base.json", ""
	if stage == stageEvaluateResult {
		name, model = "result.json", m.jobPath(j.ID, outputSubdir, j.Result.File)
	}
	report := evalSubdir + "/" + name
	_ = jr.Remove(report) // a report of an earlier attempt must not count for this one
	dataset, out := plan.validation, m.jobPath(j.ID, evalSubdir, name)
	places := map[string]string{"{evalDataset}": dataset, "{evalModel}": model, "{evalOutput}": out,
		"{jobId}": j.ID, "{jobDir}": m.jobPath(j.ID)}
	run := &commandRun{spec: spec, argv: buildArgv(spec.Args, plan.evalArg, places),
		env: map[string]string{"MDDB_EVAL_DATASET": dataset, "MDDB_EVAL_MODEL": model, "MDDB_EVAL_OUTPUT": out,
			"MDDB_TRAIN_JOB_ID": j.ID, "MDDB_TRAIN_TRIAL": trialFlag(j.Trial)},
		dir: workDir(spec, m.jobPath(j.ID)), timeout: spec.timeout, log: log}
	code, err := run.run(ctx)
	if ctx.Err() != nil {
		return context.Cause(ctx)
	}
	st := &evalStage{Status: BulkJobCompleted, ExitCode: &code}
	if err == nil {
		st.Report, err = readReport(jr, report)
	}
	if err != nil {
		st.Status, st.Error = BulkJobFailed, err.Error()
		_, _ = fmt.Fprintf(log, "evaluation failed: %v\n", err)
	}
	m.update(j, func() { // planEvaluation made j.Evaluation
		if stage == stageEvaluateBase {
			j.Evaluation.Base = st
		} else {
			j.Evaluation.Result = st
		}
		j.Evaluation.compare()
	})
	return nil
}

// readReport reads and checks a report the command wrote.
func readReport(jr *os.Root, name string) (*evalReport, error) {
	f, _, err := openRegular(jr, name)
	if err != nil {
		return nil, fmt.Errorf("the evaluate command wrote no report at MDDB_EVAL_OUTPUT: %w", err)
	}
	defer func() { _ = f.Close() }()
	return readEvalReport(f)
}

// evaluateStages evaluates a finished run again, with the dataset it used.
func (m *trainingManager) evaluateStages(ctx context.Context, plan *runPlan, jr *os.Root, log *trainingLog) error {
	j := plan.job
	plan.train, plan.validation = m.jobPath(j.DatasetID, datasetSubdir, trainFile), m.jobPath(j.DatasetID, datasetSubdir, validationFile)
	if j.Trial {
		plan.train, plan.validation = m.jobPath(j.ID, trialSubdir, trainFile), m.jobPath(j.ID, trialSubdir, validationFile)
	}
	_ = jr.MkdirAll(evalSubdir, 0o750) // a failure shows when the report is read
	again := *j
	again.Evaluate = nil // asked for explicitly now
	if !m.planEvaluation(&again, plan.profile) {
		m.update(j, func() { j.Evaluation = again.Evaluation })
		return nil
	}
	m.update(j, func() { j.Evaluation = &trainingEvaluation{} })
	var err error
	if plan.profile.Evaluate.Baseline {
		err = m.evaluateStage(ctx, plan, jr, log, stageEvaluateBase)
	}
	if err == nil {
		err = m.evaluateStage(ctx, plan, jr, log, stageEvaluateResult)
	}
	return err
}
