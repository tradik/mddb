---
title: "From Collections to a Model File"
slug: "blog/from-collections-to-a-model-file"
status: publish
type: post
date: 2026-11-22
tags: [rag, retrieval, operations]
categories: [Guides]
excerpt: "MDDB 2.19.11 turns collections into a training dataset and starts the training tool you already have. A recipe collection shows what a tuned model file is for, what search already does better, and where the line between the two runs."
description: "MDDB 2.19.11 model training: build a JSONL dataset from one or several collections, start your own trainer command, compare the model before and after, and download the GGUF file."
---

You have a few hundred recipes in MDDB. Two requests sound alike and are
not: "find me a vegan soup with leeks" and "write me a new vegan soup with
leeks, in the way we write recipes". The first is search. MDDB answers it
today and no model file is involved. The second is writing, and a small
model tuned on your recipes does it in your layout and your voice, where a
general model does it in its own.

2.19.11 adds the step between the two: it turns collections into a training
dataset and starts a training tool for you. It does not train anything
itself.

## What a GGUF file is

A GGUF file is one file that holds a model's weights and what a program
needs to run them on your own machine. The same model can be stored at
different precision: fewer bits per weight give a smaller file and a little
less fidelity. The file is a container. It says nothing about how the model
was trained or what it is good at.

Tuning teaches a model a skill or a style from examples. It does not turn
the model into a database: a tuned model does not reliably remember your
recipe for leek soup, and it will not know about the one you added
yesterday. Facts that change belong in search. The strongest setup uses
both: the tuned model writes, and MDDB search supplies the real recipes to
keep it honest.

## What MDDB does, and what it leaves to your tool

| Step | Who does it |
|---|---|
| Pick collections, build a dataset (JSON Lines) | MDDB |
| Load a base model, tune it, merge, write the GGUF file | your trainer command |
| Score the model before and after on held-back examples | your evaluate command, started by MDDB |
| Keep the log, show progress, offer the file for download | MDDB |

MDDB gains no machine-learning dependency. A server operator describes a
trainer in a file: the command, the parameters it accepts and their limits.
The panel and the API choose a trainer by name and set those parameters.
They cannot set a command or a path. A database that started whatever
program a request named would be a remote shell.

The module is called `modelTraining` and starts switched off.

## The recipe example, run on a server

Everything below ran on a local server built from the 2.19.11 source, with
two stand-in scripts in place of a real trainer: they follow the contract
and write a placeholder file. The scores are theirs, not a model's.

Twelve recipes went into a `recipes` collection, each with its text as
content and `ingredients`, `cuisine` and `diet` as metadata. The dataset is
built from a template over those fields:

```bash
curl -X POST http://localhost:11023/v1/training/datasets -d '{
  "name": "recipes-v1", "collections": ["recipes"], "format": "pairs",
  "pairs": {"input": "Ingredients: {meta.ingredients}. Cuisine: {meta.cuisine}. Diet: {meta.diet}.",
            "output": "{content}", "system": "You write recipes in our house style."},
  "validationRatio": 0.2, "shuffle": true, "seed": 1, "dedupe": true,
  "acknowledgeUnlicensed": true}'
```

Without the last field the server refuses, because it has no licence on
record for the collection:

```
licence acknowledgement required: recipes record no licence; check you may
train on them and send acknowledgeUnlicensed: true
```

A collection installed from a corpus pack carries its licence, and the
dataset summary lists it. The summary of this one: 12 records, 10 for
training, 2 held back for validation, 3,625 bytes, no duplicates. One record:

```json
{"system": "You write recipes in our house style.",
 "input": "Ingredients: rice, saffron, peas, herb5. Cuisine: spanish. Diet: vegetarian.",
 "output": "# Saffron rice 5\n\n1. Toast the rice.\n2. Add stock and saffron.\n3. Stir in peas.",
 "meta": {"collection": "recipes", "key": "saffron-rice-5", "lang": "en"}}
```

`"collections": ["recipes", "techniques"]` builds one dataset from several
collections, and each record says which one it came from.

A **trial run** comes first. It uses a small sample and the trainer's trial
settings (one epoch here), so a mistake in the setup costs seconds, not an
afternoon:

```bash
curl -X POST http://localhost:11023/v1/training/jobs \
  -d '{"profile": "recipes", "datasetId": "ds_6d6912e4f37e5d9b", "trial": true}'
```

Then the full run, with the one parameter this trainer declares:

```bash
curl -X POST http://localhost:11023/v1/training/jobs \
  -d '{"name": "recipes v1", "profile": "recipes", "datasetId": "ds_6d6912e4f37e5d9b",
       "parameters": {"epochs": 3}}'
```

The job reports its stage, progress and log. When it ends it carries the
result file and the two evaluations:

```json
{"status": "completed", "result": {"file": "recipes.gguf", "bytes": 38},
 "evaluation": {"base": {"report": {"total": 2, "score": 0.2}},
                "result": {"report": {"total": 2, "score": 0.6}},
                "delta": 0.4}}
```

`GET /v1/training/jobs/{id}/download` returns the file. When the tuned score
is not higher than the base score, the job and the panel say so in plain
words. That check matters more than any training parameter: a file that
downloads and runs is easy to mistake for a model that got better.

What the server refuses, as observed:

| Request | Answer |
|---|---|
| `"epochs": "3; rm -rf /"` | `400` epochs: a number |
| `"epochs": 99` | `400` epochs: from 1 to 10 |
| `"command": "/bin/sh"` as a parameter | `400` unknown parameter "command" |
| a second run while one is going | `409` the profile already runs as many jobs as it allows (1) |
| any training route while the module is off | `404` feature disabled |

The panel has the same flow on **Collections → Model Training**: build a
dataset, pick a trainer, press *Trial run* or *Start*, read the comparison.

## When it is worth doing

Worth it:

- you want output in a fixed layout or voice, and a general model keeps
  drifting from it;
- the task is a skill with clear right answers (a reasoning pattern, a
  format conversion, a classification), and you have hundreds or thousands
  of consistent examples;
- the model must run locally, without sending text elsewhere.

Not worth it:

- you want the model to "know" your documents. Connect it to search
  instead ([MDDB as Your Knowledge Base](/blog/mddb-as-a-knowledge-base/));
  the documents stay current and the answers can cite them;
- you have a few dozen examples, or examples that disagree with each other;
- nobody will look at the before-and-after comparison.

Check the licences of what you train on. A dataset built from other
people's text carries their terms into the model.

The step-by-step version, the trainer file format and the contract a command
must meet are in the [Model Training Guide](/docs/model-training-guide/) and
[Model Training](/docs/model-training/).
