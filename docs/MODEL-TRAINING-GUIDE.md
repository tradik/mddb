---
title: "Making a Model File from Your Collections"
slug: "docs/model-training-guide"
description: "A plain-language guide to GGUF model files: what they are, what tuning changes, and a worked example that turns a recipes collection into a training dataset, runs a trainer, compares scores before and after, and downloads the file."
status: publish
---

# Making a model file from your collections

This guide is for people who have never made a model file. It explains what
a GGUF file is, what tuning a model can and cannot do, and walks through one
example end to end: recipes. The reference for every option is
[Model Training](MODEL-TRAINING.md).

## 1. What a GGUF file is and what it is for

A language model is, in the end, a very large table of numbers called
*weights*. GGUF is a **file format**: one file that holds those weights plus
what a program needs to run them on your own computer (the vocabulary, the
model's layout, its settings). The weights are the model; GGUF is the box it
travels in. The same model can be stored in other formats, and one GGUF file
can be opened by any local runner that reads the format.

Most GGUF files are *quantised*: each weight is stored with fewer bits than
the model was trained with. That is a trade between size and quality:

| Bits per weight | File size | Fidelity to the original |
|---|---|---|
| more (for example 16 or 8) | larger | closest |
| middle (for example 5 or 4) | about a third to a quarter of 16-bit | small, usually acceptable loss |
| fewer (for example 3 or 2) | smallest | noticeable loss |

Names such as `Q4` or `Q8` in a file name say which level was used.

## 2. What tuning changes, and what it does not

Tuning (fine-tuning) shows a model many examples of one task until it picks
up the **skill or the style**: your layout, your tone, your way of answering.
It does not turn the model into a database. A tuned model does not reliably
remember each recipe you showed it, and it will invent details with
confidence.

Be clear about what you want:

- **Finding a recipe you already have** is search. MDDB does that today,
  without any model file: full-text, vector and hybrid search over your
  collection.
- **Writing a new recipe** in your format — given ingredients, a cuisine or
  a constraint such as "vegan, under 30 minutes" — is what a tuned model is for.

The strongest setup is both: the tuned model writes, and MDDB search, over
MCP or a chat scenario, gives it the real recipes and facts to lean on (see
[LLM connections](LLM_CONNECTIONS.md)).

## 3. The path

1. **Collections** — your documents in MDDB. *(MDDB)*
2. **Dataset** — MDDB turns them into a JSON Lines file, with a part held
   back for checking. *(MDDB)*
3. **Your trainer command** — a program your server's operator installed:
   it loads a base model, tunes it on the dataset, merges the result and
   writes a GGUF file. *(your tool; MDDB only starts it)*
4. **Evaluation before and after** — an evaluate command scores the base
   model and the tuned one on the held-back part. *(your tool; MDDB starts it
   and shows the scores)*
5. **Download** the `.gguf` file. *(MDDB)*
6. **Use it** in a local runner of your choice. *(you)*

## 4. Worked example: recipes

The commands use `http://localhost:11023`. With authentication on, add
`-H "Authorization: Bearer $TOKEN"` with an administrator's token to each.

**Load recipes.** Each document has the recipe as content and metadata.
Two of them (load as many as you have the same way):

```bash
curl -X POST http://localhost:11023/v1/add -d '{"collection":"recipes","key":"leek-soup","lang":"en",
  "meta":{"title":["Leek soup"],"ingredients":["leeks","potatoes","stock"],"cuisine":["french"],
          "diet":["vegan"],"timeMinutes":["40"],"license":["CC-BY-4.0"]},
  "contentMd":"# Leek soup\n\n1. Sweat the leeks.\n2. Add potatoes and stock, simmer 25 minutes.\n3. Blend."}'
curl -X POST http://localhost:11023/v1/add -d '{"collection":"recipes","key":"tomato-pasta","lang":"en",
  "meta":{"title":["Tomato pasta"],"ingredients":["pasta","tomatoes","garlic"],"cuisine":["italian"],
          "diet":["vegetarian"],"timeMinutes":["20"],"license":["CC-BY-4.0"]},
  "contentMd":"# Tomato pasta\n\n1. Cook the pasta.\n2. Fry garlic, add tomatoes.\n3. Toss together."}'
```

A folder of Markdown files with front matter loads with `mddb-cli autoindex`
instead. A second collection, say `techniques`, can go into the same dataset.

**Enable the module.** On the panel's **Features** page switch on *Model
training*, or:

```bash
curl -X PUT http://localhost:11023/v1/features/modelTraining -d '{"enabled": true}'
```

**Describe the trainer.** The server's operator writes a profile file and
starts MDDB with `MDDB_TRAINERS_CONFIG=/etc/mddb/trainers.yaml`:

```yaml
trainers:
  - name: recipes
    description: Writes recipes in our house style
    command: /opt/trainers/train.sh
    output: "*.gguf"
    progress: PROGRESS
    parameters:
      - {name: epochs, type: int, min: 1, max: 10, default: 3, args: ["--epochs", "{value}"]}
    trialRecords: 50
    trialParameters: {epochs: 1}
    evaluate:
      command: /opt/trainers/evaluate.sh
      baseline: true
```

To try the whole flow before you have a real tool, these two stand-in
scripts follow the contract (make them executable with `chmod +x`). They
write a placeholder file, not a usable model:

```sh
#!/bin/sh
# /opt/trainers/train.sh: replace the middle with your own tool.
set -eu
echo "PROGRESS 10 reading $(wc -l < "$MDDB_TRAIN_DATASET") records, args: $*"
printf 'stand-in for job %s\n' "$MDDB_TRAIN_JOB_ID" > "$MDDB_TRAIN_OUTPUT_DIR/recipes.gguf"
echo "PROGRESS 100 done"
```

```sh
#!/bin/sh
# /opt/trainers/evaluate.sh: a real one scores MDDB_EVAL_MODEL on MDDB_EVAL_DATASET.
set -eu
total=$(wc -l < "$MDDB_EVAL_DATASET")
if [ -z "$MDDB_EVAL_MODEL" ]; then score=0.2; else score=0.6; fi
printf '{"total":%s,"correct":0,"score":%s,"metrics":{"standIn":%s}}\n' "$total" "$score" "$score" > "$MDDB_EVAL_OUTPUT"
```

**Build a pairs dataset.** The input describes what you want, the output is
the recipe:

```bash
curl -X POST http://localhost:11023/v1/training/datasets -d '{"name":"recipes-v1","collections":["recipes"],
  "format":"pairs","pairs":{"input":"Ingredients: {meta.ingredients}. Cuisine: {meta.cuisine}. Diet: {meta.diet}.",
  "output":"{content}","system":"You write recipes in our house style."},
  "validationRatio":0.2,"shuffle":true,"seed":1,"dedupe":true,"acknowledgeUnlicensed":true}'
```

Add `"filterMeta":{"cuisine":["italian"]}` to train on one cuisine only, or
`"collections":["recipes","techniques"]` for several. The `license` field of
your documents is your note; MDDB records a licence per *collection* only
for collections installed from a [corpus pack](CORPUS-PACKS.md), so here it
asks for `acknowledgeUnlicensed` and lists `recipes` under
`unlicensedAcknowledged`. `GET /v1/training/datasets/{id}` shows the summary;
`…/download?part=train` and `?part=validation` give the two parts. Three
records of a dataset of three recipes (shuffled, so your order may differ):

```json
{"system":"You write recipes in our house style.","input":"Ingredients: leeks, potatoes, stock. Cuisine: french. Diet: vegan.","output":"# Leek soup\n\n1. Sweat the leeks.\n2. Add potatoes and stock, simmer 25 minutes.\n3. Blend.","meta":{"collection":"recipes","key":"leek-soup","lang":"en"}}
{"system":"You write recipes in our house style.","input":"Ingredients: pasta, tomatoes, garlic. Cuisine: italian. Diet: vegetarian.","output":"# Tomato pasta\n\n1. Cook the pasta.\n2. Fry garlic, add tomatoes.\n3. Toss together.","meta":{"collection":"recipes","key":"tomato-pasta","lang":"en"}}
{"system":"You write recipes in our house style.","input":"Ingredients: rice, saffron, peas. Cuisine: spanish. Diet: vegetarian.","output":"# Saffron rice\n\n1. Toast the rice.\n2. Add stock and saffron.\n3. Stir in peas.","meta":{"collection":"recipes","key":"saffron-rice","lang":"en"}}
```

**A trial run, then the full run.** The trial uses at most `trialRecords`
records and the profile's `trialParameters` (one epoch here), and tells the
command `MDDB_TRAIN_TRIAL=1`:

```bash
curl -X POST http://localhost:11023/v1/training/jobs -d '{"profile":"recipes","datasetId":"ds_…","trial":true}'
curl -X POST http://localhost:11023/v1/training/jobs -d '{"name":"recipes v1","profile":"recipes","datasetId":"ds_…","parameters":{"epochs":3}}'
curl http://localhost:11023/v1/training/jobs/run_…
```

Start the full run once the trial is done: a profile runs one job at a time
unless its `maxConcurrent` allows more (a second one answers `409`). The
answer of `GET` has the status, the stage, the progress, the exit code and
the end of the log, with one section per stage (`=== training …`).

**Read the evaluation.** `evaluation.base.report.score` is the base model,
`evaluation.result.report.score` the tuned one, `delta` the difference. When
the tuned score is not higher, the job says so. Then: give more or cleaner
examples (consistent layout, no half-finished recipes), try fewer epochs (too
many make a model repeat its examples), and check that the validation part
resembles what you will ask for.

**Download and use it.** `GET /v1/training/jobs/run_…/download` gives
`recipes.gguf`. Open it in a local runner of your choice; then connect that
runner's chat to MDDB as described in [LLM connections](LLM_CONNECTIONS.md),
so the model can look real recipes up while it writes new ones.

**The same in the panel.** Open **Collections → Model Training**. In *Build a
dataset* pick **Collections**, keep **Format** on *Pairs*, fill **Input**,
**Output** and **System text**, set **Validation share**, tick the licence
box if asked, and press **Build dataset**; the dataset appears under
*Datasets* with its summary, its first records and **Training part** /
**Validation part** downloads. In *Start a run* choose the **Trainer** and
the **Dataset**, set the parameters, and press **Trial run**, then **Start**.
Under *Runs* pick a run to see its log and the scores *Before*, *After* and
*Difference*, with **Cancel**, the result's download button and **Evaluate
again**.

## 5. How many examples, how good

- Quality and consistency matter more than count: every example should look
  the way you want the answers to look.
- Keep a validation part (the panel suggests one tenth for pairs). Without
  it there is nothing to score the result on.
- One clear task per dataset. Writing recipes and answering cooking
  questions are two datasets.
- Watch licences: train only on what you may use that way.

## 6. Troubleshooting

| You see | Why, and what to do |
|---|---|
| `404 {"error":"feature disabled","feature":"modelTraining"}` | The module is off: switch it on (Features). If it stays locked, the operator set `MDDB_FEATURES_FORCE_OFF` |
| `409` "no trainer is configured" | `MDDB_TRAINERS_CONFIG` is unset, or every profile in it was refused; `GET /v1/training/profiles` lists why under `errors` |
| `400` naming a parameter | The value is outside what the profile allows (range, list, length, characters), or the profile does not have that parameter |
| `400` "record no licence" | Check you may train on those collections, then send `acknowledgeUnlicensed: true` (the panel's licence box) |
| "Not evaluated: …" | The profile has no evaluate command, the dataset is *Text*, or it has no validation part |
| Status *Interrupted by a server restart* | MDDB stopped while the job ran; start it again |
