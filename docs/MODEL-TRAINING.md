---
title: "Model Training"
slug: "docs/model-training"
description: "Export collections as a training dataset and start the trainer command your server's operator configured: dataset formats, the trainer profile file, the contract a command meets, trial runs, before/after evaluation, licences and the security model."
status: publish
---

# Model Training

MDDB holds your data in collections. The model training module turns one or
several of them into a training dataset and starts a training program that
the server's operator installed and named in a configuration file. MDDB
keeps the run as a job, with its log and the file it produced, and lets an
administrator download that file.

New to model files? Read the step-by-step guide first:
[How to make a model file (GGUF) from your collections](MODEL-TRAINING-GUIDE.md).

## What it does and does not do

It does:

- export collections as JSON Lines, in two formats (`text` and `pairs`), with
  a filter, a language, a limit, a seeded shuffle, a validation split and
  exact-duplicate removal;
- record each collection's licence on every record and in the summary;
- start a command the operator configured, with parameter values checked
  against what the operator declared, and keep its log, exit code and result;
- run it as a *trial* on a small sample first, if you ask;
- run the operator's *evaluate* command before and after training and show
  the two scores side by side.

It does not:

- train anything itself, or ship any training or inference library: MDDB has
  no machine-learning dependency for this;
- let anyone set or change a command, a path, an argument or an environment
  variable through the API, the panel or MCP;
- turn vectors into training data: the export is text;
- write questions and answers with a model: `pairs` are built from fields
  your documents already have;
- remove near-duplicates (only exact ones, by content hash), or split
  documents into chunks (one record per document).

## Enabling it

The module `modelTraining` is **off** by default. An administrator turns it
on on the panel's **Features** page (category *Data*) or with
`PUT /v1/features/modelTraining {"enabled": true}`. `MDDB_FEATURES_FORCE_OFF=modelTraining`
keeps it off whatever the switch says. While it is off every
`/v1/training/*` route answers `404 {"error":"feature disabled","feature":"modelTraining"}`
and the panel hides its page.

| Variable | Default | What it sets |
|---|---|---|
| `MDDB_TRAINERS_CONFIG` | unset | Path of the trainer profile file (YAML or JSON). Unset: datasets can be built, starting a run answers `409` "no trainer is configured" |
| `MDDB_TRAINING_DIR` | `training` beside the database | Where every job gets its directory: datasets, trial cuts, outputs, reports, logs |

The profile file is read once at startup. A profile that fails validation is
left out and reported in the log and in `GET /v1/training/profiles`
(`errors`); the others load.

## The trainer profile file

```yaml
trainers:
  - name: recipes-small
    description: Tunes the base model on recipe pairs
    command: /opt/trainers/train.sh          # absolute path, an executable file
    args: ["--data", "{dataset}", "--validation", "{validation}", "--out", "{outputDir}"]
    output: "*.gguf"                         # the result: a file name pattern in the output directory
    timeout: 12h                             # default 24h
    maxConcurrent: 1                         # runs of this profile at once (default 1)
    progress: PROGRESS                       # optional: lines "PROGRESS <percent> [message]"
    env: [PATH, HOME, LANG]                  # passed through from the server; default PATH, HOME, LANG, LC_ALL, TZ, TMPDIR, SYSTEMROOT, TEMP, TMP
    workDir: /opt/trainers                   # optional; default the job directory
    parameters:
      - {name: epochs, type: int, min: 1, max: 20, default: 3, args: ["--epochs", "{value}"]}
      - {name: learningRate, type: float, min: 0.00001, max: 0.01, default: 0.0002, args: ["--lr={value}"]}
      - {name: quantisation, type: enum, values: [q4, q5, q8], default: q4, args: ["--quant", "{value}"]}
      - {name: packing, type: bool, args: ["--packing"]}       # added when true
      - {name: runName, type: string, maxLength: 32, args: ["--name", "{value}"]}
    trialRecords: 100                        # a trial's sample (default 100)
    trialTimeout: 15m                        # default: timeout
    trialParameters: {epochs: 1}             # win over the request's values in a trial
    evaluate:                                # optional, validated and started like command
      command: /opt/trainers/evaluate.sh
      args: ["--data", "{evalDataset}", "--model", "{evalModel}", "--report", "{evalOutput}"]
      baseline: true                         # it can also score the base model (empty model)
      timeout: 1h                            # default 1h
      parameters:
        - {name: limit, type: int, min: 1, max: 1000, default: 200, args: ["--limit={value}"]}
```

Unknown keys are errors. Rules checked at startup:

- `name`: 1 to 64 letters, digits, `.`, `-`, `_`; unique.
- `command`: an absolute, clean path to an existing regular file, executable
  outside Windows. It is never looked up in `PATH` and never run by a shell.
- `args` and each parameter's `args`: one line each; the placeholders are
  `{dataset}`, `{validation}`, `{outputDir}`, `{jobId}`, `{jobDir}`,
  `{evalDataset}`, `{evalModel}`, `{evalOutput}`, and `{value}` in a
  parameter's own arguments. Anything else in braces is refused.
- parameters: `type` is `string`, `int`, `float`, `bool` or `enum`; `min` ≤
  `max`; an enum lists its `values`; a default must pass its own checks;
  `required: true` makes a value mandatory when there is no default.
- strings: at most `maxLength` bytes (default 256, at most 4096), no NUL or
  line break, and the conservative set `A–Z a–z 0–9 . _ , : + = @ -` without
  a leading `-` unless the parameter gives its own `pattern` (a regular
  expression).
- `env`: variable names only; a name starting with `MDDB_` is refused, so the
  server's own settings and secrets never reach a command.
- `output`: a file name pattern without a directory.

## The contract a trainer command meets

MDDB starts `command` with `args`, then each parameter's arguments in the
order the profile declares them, in the working directory, with this
environment (the allowlist plus):

| Variable | Value |
|---|---|
| `MDDB_TRAIN_DATASET` | Absolute path of the training part (JSON Lines) |
| `MDDB_TRAIN_VALIDATION` | Absolute path of the validation part (may be empty) |
| `MDDB_TRAIN_OUTPUT_DIR` | The directory to write the result into; it exists |
| `MDDB_TRAIN_JOB_ID` | The job's id |
| `MDDB_TRAIN_TRIAL` | `1` in a trial run, `0` otherwise |

The command:

1. reads the dataset;
2. writes its result into `MDDB_TRAIN_OUTPUT_DIR` as a regular file whose name
   matches `output` (the first match by name is the result; a symbolic link
   does not count);
3. may print `PROGRESS <percent> [message]` lines (the profile's `progress`
   prefix), which become the job's progress;
4. exits `0` on success. Any other exit code fails the run; exit `0` without
   a matching file fails it too.

Standard output and standard error go to the job's log, kept up to 8 MiB.
A neutral example, `/opt/trainers/train.sh`:

```sh
#!/bin/sh
# Called by MDDB: --data <train.jsonl> --validation <validation.jsonl> --out <dir> --epochs N ...
set -eu
echo "PROGRESS 0 starting"
# ... run your own tool here: load the base model, tune it on "$MDDB_TRAIN_DATASET",
# merge, convert to the model file format ...
echo "PROGRESS 100 done"
cp /path/where/your/tool/wrote/model.gguf "$MDDB_TRAIN_OUTPUT_DIR/model.gguf"
```

## The evaluate command and its report

When the profile has `evaluate`, the dataset is `pairs` and it has a
validation part, a run has two more stages, each with its own log section:
`evaluate-base` before training (only with `baseline: true`) and
`evaluate-result` after a successful training. `"evaluate": false` in the
request skips both. The command gets `MDDB_EVAL_DATASET` (the validation
part: records with `input` and the expected `output`), `MDDB_EVAL_MODEL`
(empty for the base model, the result file for the tuned one),
`MDDB_EVAL_OUTPUT` (where to write the report, inside the job directory),
`MDDB_TRAIN_JOB_ID` and `MDDB_TRAIN_TRIAL`. It writes:

```json
{"total": 200, "correct": 143, "score": 0.715,
 "metrics": {"exactMatch": 0.715, "ingredientsCovered": 0.88},
 "samples": [{"input": "...", "expected": "...", "actual": "...", "correct": true}]}
```

MDDB checks it: at most 1 MiB, `total` and `correct` whole numbers with
`correct` ≤ `total`, `score` from 0 to 1, at most 32 metrics with finite
values, at most 20 samples kept, each text cut to 2000 characters. The report
is stored on the job as data; nothing in it is executed or rendered as
markup. A report that is missing, malformed, a symbolic link, or a non-zero
exit marks that stage failed and the run `completedWithWarnings`: the result
stays downloadable. The job shows the score before, after, the difference,
and a plain note when the tuned score is not higher or there is no baseline.
Without a validation part (a `text` dataset, or `validationRatio` 0) the
evaluation is skipped and the job says why. `POST /v1/training/jobs/{id}/evaluate`
runs it again on a finished run; there is no endpoint that evaluates a file
you name.

## Dataset formats

`text`, one record per document:

```json
{"text": "Leek soup\n\nSweat the leeks...", "meta": {"collection": "recipes", "key": "leek-soup", "lang": "en", "license": "CC-BY-4.0"}}
```

`pairs`, built from templates over `{content}`, `{key}`, `{lang}`,
`{collection}` and `{meta.<name>}` (several values of a metadata key are
joined with `, `). A document whose template names an empty field is
skipped and counted:

```json
{"system": "You write recipes.", "input": "A french dish, vegan, with leeks, potatoes", "output": "Leek soup\n\nSweat the leeks...", "meta": {"collection": "recipes", "key": "leek-soup", "lang": "en", "license": "CC-BY-4.0"}}
```

Options of `POST /v1/training/datasets`: `collections` (1 to 16),
`format`, `pairs {input, output, system}`, `filterMeta` (as `/v1/export`),
`lang`, `maxRecords` (0 = all, at most 1,000,000), `shuffle` and `seed`
(the same seed gives the same order), `validationRatio` (0 to 0.5, the last
records after the shuffle; at least one when above 0), `dedupe` (default
true: exact duplicates by content hash, whatever collection they came
from), `acknowledgeUnlicensed`. The summary gives records, the split, bytes,
per collection the documents read, records kept, duplicates and skipped,
its licence, and a preview of the first records.

## Licences

Every record carries its collection's licence when the collection records
one (a collection installed from a [corpus pack](CORPUS-PACKS.md) does). A
collection without licence information is refused unless the request sends
`"acknowledgeUnlicensed": true`; the summary lists which collections that
covered (`unlicensedAcknowledged`). Checking that the terms allow training
is yours to do.

## API

All routes are admin only, REST only (no gRPC, GraphQL or MCP tool), and
under the module switch. See [openapi.yaml](openapi.yaml), tag *Model Training*.

| Route | What it does |
|---|---|
| `GET /v1/training/profiles` | `{configured, profiles, errors}`: names, descriptions, parameters, trial and evaluate settings, runs now. Never the command line |
| `GET /v1/training/datasets` | Every dataset, newest first |
| `POST /v1/training/datasets` | Build a dataset: `202 {jobId, job}`; `400` invalid or licence not acknowledged; `507` not enough disk |
| `GET /v1/training/datasets/{id}` | Status, summary and preview |
| `GET /v1/training/datasets/{id}/download?part=train\|validation` | A part of a completed dataset |
| `DELETE /v1/training/datasets/{id}` | Delete it; `409` while it builds or a running job uses it |
| `GET /v1/training/jobs` | Every run, newest first, `trial` marked |
| `POST /v1/training/jobs` | `{name, profile, datasetId \| dataset, parameters, trial, evaluate, evaluateParameters}` → `202 {jobId, job}`; `400` unknown profile or parameter, refused value; `409` no trainer configured or the profile is busy |
| `GET /v1/training/jobs/{id}` | Status, stage, progress, exit code, result, evaluation, and the last 16 KiB of the log |
| `POST /v1/training/jobs/{id}/cancel` | Cancel |
| `POST /v1/training/jobs/{id}/evaluate` | Evaluate a finished run again |
| `GET /v1/training/jobs/{id}/download` | The result file |
| `DELETE /v1/training/jobs/{id}` | Delete a finished run and its directory |

Statuses: `pending`, `processing`, `completed`, `completedWithWarnings`,
`failed`, `cancelled`, `interrupted` (the server stopped while the job ran;
a job found running at startup is marked so). Stages of a run: `dataset`,
`trial`, `evaluate-base`, `training`, `evaluate-result`. The panel reads a
running job every two seconds; job events are not sent on `/v1/events`.

## Security model

- **Commands come only from the server's configuration.** The API picks a
  profile by name and sends parameter values; no route, panel field or MCP
  tool sets a command, path, argument template or environment variable.
- **No shell.** The command is started with an argument list. Each value is
  checked against its declared type, range, enum or pattern and becomes its
  own argument; text it contains is never expanded again. Values with NUL or
  line breaks, over-long strings and strings outside the character set are
  refused.
- **Paths.** Every job has a directory under `MDDB_TRAINING_DIR`; MDDB
  reaches every file in it through an `os.Root`, so a job id, a result name
  or a link cannot lead outside. A result, dataset part or report is served
  or read only when it is a regular file, not a symbolic link, and a result
  only when its name matches the profile's pattern.
- **Who.** Admin only on every route, in the HTTP access rules; a request
  made with a scoped MCP key is refused; no MCP tool, gRPC method or GraphQL
  field reaches the module. With authentication off, as for backups, anyone
  who reaches the API can start a configured trainer: turn authentication on
  before enabling the module on a shared network.
- **Limits.** Per-profile concurrency (`409` past it), a timeout, a capped
  log, a disk-space check before an export (256 MiB stay free), cancel:
  `SIGTERM` to the command's process group, `SIGKILL` 10 s later; on Windows
  the process is killed (its children are not reached). The environment is
  the allowlist plus the job's variables, never an `MDDB_*` setting.
- **Audit.** Building a dataset, starting, cancelling, re-evaluating,
  downloading and deleting are written to the audit log with the user
  (`training.dataset`, `training.start`, `training.cancel`,
  `training.evaluate`, `training.download`, `training.delete`).
- **What MDDB does not do:** it does not sandbox the command. It runs with
  the server's user and rights; run the server, or the trainer, as a user
  that can reach only what training needs.
