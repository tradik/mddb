package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"
)

// A training run: the dataset (built here when the request carries one),
// a trial's cut of it, the base model's evaluation, the operator's trainer,
// the result file, and the tuned model's evaluation — each a stage with its
// section of the job's log. An evaluation that fails leaves the run
// completedWithWarnings: the result stays downloadable.

// runRequest is POST /v1/training/jobs.
type runRequest struct {
	Name               string          `json:"name,omitempty"`
	Profile            string          `json:"profile"`
	DatasetID          string          `json:"datasetId,omitempty"`
	Dataset            *datasetRequest `json:"dataset,omitempty"`
	Parameters         map[string]any  `json:"parameters,omitempty"`
	Trial              bool            `json:"trial,omitempty"`
	Evaluate           *bool           `json:"evaluate,omitempty"`
	EvaluateParameters map[string]any  `json:"evaluateParameters,omitempty"`
}

// runPlan is a checked run, ready to start.
type runPlan struct {
	job     *trainingJob
	profile *trainerProfile
	params  []paramArgs
	evalArg []paramArgs
	inline  *preparedDataset
	dataset *trainingJob
	// paths of the dataset parts the stages read
	train, validation string
}

// invalid wraps a refusal of the request.
func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", errTrainingInvalid, fmt.Sprintf(format, args...))
}

// profileFor is the named profile; no profiles at all is errTrainingNoTrainer.
func (m *trainingManager) profileFor(name string) (*trainerProfile, error) {
	if len(m.profiles.list) == 0 {
		return nil, errTrainingNoTrainer
	}
	p, ok := m.profiles.byName(name)
	if !ok {
		return nil, invalid("unknown profile %q", name)
	}
	return p, nil
}

// submitRun checks a run request and starts it.
func (m *trainingManager) submitRun(req runRequest, user string) (*trainingJob, error) {
	p, err := m.profileFor(req.Profile)
	if err != nil {
		return nil, err
	}
	plan := &runPlan{profile: p}
	var overrides map[string]any
	if req.Trial {
		overrides = p.TrialParameters
	}
	used, params, err := resolveParams(p.Parameters, req.Parameters, overrides)
	if err != nil {
		return nil, invalid("%v", err)
	}
	plan.params = params
	if plan.evalArg, err = evalParams(p, req.EvaluateParameters); err != nil {
		return nil, err
	}
	if (req.DatasetID == "") == (req.Dataset == nil) {
		return nil, invalid("name a built dataset (datasetId) or describe one (dataset), not both")
	}
	now := time.Now().Unix()
	if req.Dataset != nil {
		if plan.inline, err = m.prepareDataset(*req.Dataset); err != nil {
			return nil, err
		}
		plan.dataset = &trainingJob{ID: newTrainingID(trainingKindDataset), Kind: trainingKindDataset,
			Name: req.Dataset.Name, Status: BulkJobPending, User: user, SubmittedAt: now}
	} else if plan.dataset, err = m.jobOfKind(req.DatasetID, trainingKindDataset); err != nil || plan.dataset.Status != BulkJobCompleted {
		return nil, invalid("dataset %q is not a completed dataset", req.DatasetID)
	}
	if err := m.acquireSlot(p); err != nil {
		return nil, err
	}
	j := &trainingJob{ID: newTrainingID(trainingKindRun), Kind: trainingKindRun, Name: req.Name, Status: BulkJobPending,
		Profile: p.Name, Parameters: used, DatasetID: plan.dataset.ID, Trial: req.Trial, User: user,
		SubmittedAt: now, OutputPattern: p.Output, Evaluate: req.Evaluate, EvalParameters: req.EvaluateParameters}
	err = m.saveJob(j)
	if plan.inline != nil {
		err = errors.Join(err, m.saveJob(plan.dataset))
	}
	if err != nil {
		m.releaseSlot(p)
		return nil, err
	}
	plan.job = j
	m.launch(j.ID, func(ctx context.Context) {
		defer m.releaseSlot(p)
		m.execute(ctx, plan, m.runStages)
	})
	return j, nil
}

// evalParams checks the values for the profile's evaluate command.
func evalParams(p *trainerProfile, values map[string]any) ([]paramArgs, error) {
	if p.Evaluate == nil {
		if len(values) > 0 {
			return nil, invalid("profile %q has no evaluate command", p.Name)
		}
		return nil, nil
	}
	_, args, err := resolveParams(p.Evaluate.Parameters, values, nil)
	if err != nil {
		return nil, invalid("evaluate: %v", err)
	}
	return args, nil
}

// submitEvaluate re-runs the evaluation of a finished run.
func (m *trainingManager) submitEvaluate(id string) (*trainingJob, error) {
	j, err := m.jobOfKind(id, trainingKindRun)
	if err != nil {
		return nil, err
	}
	if (j.Status != BulkJobCompleted && j.Status != trainingDoneWithWarnings) || j.Result == nil {
		return nil, fmt.Errorf("%w: only a finished run with a result is evaluated again", errTrainingState)
	}
	p, err := m.profileFor(j.Profile)
	if err != nil {
		return nil, err
	}
	if p.Evaluate == nil {
		return nil, invalid("profile %q has no evaluate command", p.Name)
	}
	plan := &runPlan{job: j, profile: p}
	if plan.evalArg, err = evalParams(p, j.EvalParameters); err != nil {
		return nil, err
	}
	if err := m.acquireSlot(p); err != nil {
		return nil, err
	}
	m.update(j, func() { j.Status, j.Warnings, j.Error = BulkJobPending, nil, "" })
	m.launch(j.ID, func(ctx context.Context) {
		defer m.releaseSlot(p)
		m.execute(ctx, plan, m.evaluateStages)
	})
	return j, nil
}

// execute opens the job's directory and log, runs the stages and records
// how the job ended.
func (m *trainingManager) execute(ctx context.Context, plan *runPlan, stages func(context.Context, *runPlan, *os.Root, *trainingLog) error) {
	j := plan.job
	m.update(j, func() { j.Status, j.StartedAt, j.CompletedAt = BulkJobProcessing, time.Now().Unix(), 0 })
	jr, err := m.jobRoot(j.ID, true)
	if err == nil {
		defer func() { _ = jr.Close() }()
		var f *os.File
		if f, err = jr.OpenFile(trainingLogName, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o640); err == nil {
			log := &trainingLog{f: f}
			defer log.close()
			err = stages(ctx, plan, jr, log)
		}
	}
	m.update(j, func() {
		j.CompletedAt, j.Status = time.Now().Unix(), endStatus(err)
		if err != nil {
			j.Error = err.Error()
			return
		}
		if ev := j.Evaluation; ev != nil && (failedStage(ev.Base) || failedStage(ev.Result)) {
			j.Status = trainingDoneWithWarnings
			j.Warnings = append(j.Warnings, "an evaluation stage failed; the result file is kept")
		}
	})
}

// failedStage reports an evaluation stage that ran and failed.
func failedStage(st *evalStage) bool { return st != nil && st.Status == BulkJobFailed }

// stage records the stage a job is in and starts its log section.
func (m *trainingManager) stage(j *trainingJob, log *trainingLog, name string) {
	m.update(j, func() { j.Stage = name }) // progress is the trainer's and stays
	log.section(name)
}

// runStages is a run from its dataset to its tuned model's evaluation.
func (m *trainingManager) runStages(ctx context.Context, plan *runPlan, jr *os.Root, log *trainingLog) error {
	j, p := plan.job, plan.profile
	m.stage(j, log, stageDataset)
	if err := m.runDataset(ctx, plan); err != nil {
		return fmt.Errorf("dataset: %w", err)
	}
	ds := plan.dataset
	var err error
	plan.train, plan.validation = m.jobPath(ds.ID, datasetSubdir, trainFile), m.jobPath(ds.ID, datasetSubdir, validationFile)
	valCount := ds.Summary.ValidationRecords
	if j.Trial {
		m.stage(j, log, stageTrial)
		if valCount, err = m.cutTrial(plan, ds, jr); err != nil {
			return fmt.Errorf("trial: %w", err)
		}
	}
	m.update(j, func() { j.Format, j.ValidationRecords = ds.Summary.Format, valCount })
	// A directory that cannot be made shows when the command or
	// findResult reaches it.
	_ = errors.Join(jr.MkdirAll(outputSubdir, 0o750), jr.MkdirAll(evalSubdir, 0o750))
	evaluate := m.planEvaluation(j, p)
	if evaluate && p.Evaluate.Baseline {
		if err := m.evaluateStage(ctx, plan, jr, log, stageEvaluateBase); err != nil {
			return err
		}
	}
	m.stage(j, log, stageTraining)
	if err := m.train(ctx, plan, log); err != nil {
		return fmt.Errorf("training: %w", err)
	}
	res, err := findResult(jr, p.Output)
	if err != nil {
		return err
	}
	m.update(j, func() { j.Result = res })
	if evaluate {
		return m.evaluateStage(ctx, plan, jr, log, stageEvaluateResult)
	}
	return nil
}

// runDataset builds the run's own dataset; one the run names is built.
func (m *trainingManager) runDataset(ctx context.Context, plan *runPlan) error {
	if plan.inline == nil {
		return nil
	}
	return m.buildDataset(ctx, plan.dataset, plan.inline)
}

// cutTrial writes a trial's small dataset into the run's directory and
// points the stages at it; it returns the validation records kept. The cut
// is trialRecords, or the request's own limit when that is smaller.
func (m *trainingManager) cutTrial(plan *runPlan, ds *trainingJob, jr *os.Root) (int, error) {
	n := plan.profile.TrialRecords
	if mr := ds.Summary.Options.MaxRecords; mr > 0 && mr < n {
		n = mr
	}
	src, err := m.jobRoot(ds.ID, false)
	if err != nil {
		return 0, err
	}
	defer func() { _ = src.Close() }()
	_ = jr.MkdirAll(trialSubdir, 0o750) // a failure shows in the copies below
	_, err1 := cutRecords(src, jr, datasetSubdir+"/"+trainFile, trialSubdir+"/"+trainFile, n)
	kept, err2 := cutRecords(src, jr, datasetSubdir+"/"+validationFile, trialSubdir+"/"+validationFile, n)
	if err := errors.Join(err1, err2); err != nil {
		return 0, err
	}
	id := plan.job.ID
	plan.train, plan.validation = m.jobPath(id, trialSubdir, trainFile), m.jobPath(id, trialSubdir, validationFile)
	return kept, nil
}

// train runs the trainer command.
func (m *trainingManager) train(ctx context.Context, plan *runPlan, log *trainingLog) error {
	j, p := plan.job, plan.profile
	timeout := p.timeout
	if j.Trial {
		timeout = p.trialTimeout
	}
	out := m.jobPath(j.ID, outputSubdir)
	places := map[string]string{"{dataset}": plan.train, "{validation}": plan.validation, "{outputDir}": out,
		"{jobId}": j.ID, "{jobDir}": m.jobPath(j.ID)}
	run := &commandRun{spec: &p.commandSpec, argv: buildArgv(p.Args, plan.params, places),
		env: map[string]string{"MDDB_TRAIN_DATASET": plan.train, "MDDB_TRAIN_VALIDATION": plan.validation,
			"MDDB_TRAIN_OUTPUT_DIR": out, "MDDB_TRAIN_JOB_ID": j.ID, "MDDB_TRAIN_TRIAL": trialFlag(j.Trial)},
		dir: workDir(&p.commandSpec, m.jobPath(j.ID)), timeout: timeout, progress: p.Progress, log: log,
		onProgress: func(pct float64, msg string) { m.progress(j, pct, msg) }}
	code, err := run.run(ctx)
	m.update(j, func() { j.ExitCode = &code })
	return err
}

// trialFlag is MDDB_TRAIN_TRIAL.
func trialFlag(trial bool) string {
	if trial {
		return "1"
	}
	return "0"
}

// workDir is the command's working directory: its own, or the job's.
func workDir(c *commandSpec, jobDir string) string {
	if c.WorkDir != "" {
		return c.WorkDir
	}
	return jobDir
}
