package main

import (
	"bufio"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// The fake trainer and evaluate command of the model training tests: this
// test binary, started by TestMain when MDDB_TRAIN_JOB_ID is set (the job's
// own variable, so the minimal environment still carries it). It runs on
// every platform the suite runs on; the modes that make symbolic links are
// skipped on Windows, where creating one needs a privilege.

// runFakeTrainer is the fake's main: args[0] is its mode.
func runFakeTrainer(args []string) int {
	if len(args) == 0 {
		return 9
	}
	if strings.HasPrefix(args[0], "eval-") {
		return runFakeEvaluate(args[0])
	}
	out := os.Getenv("MDDB_TRAIN_OUTPUT_DIR")
	switch args[0] {
	case "train":
		fmt.Println("PROGRESS 50 halfway")
		fmt.Println("PROGRESS nonsense")
		body := strings.Join(args, "\x1f") + "\ntrial=" + os.Getenv("MDDB_TRAIN_TRIAL") +
			"\nlines=" + strconv.Itoa(countLines(os.Getenv("MDDB_TRAIN_DATASET"))) + "\nenv=" + envNames()
		// #nosec G703 -- the fake trainer writes where the test server told it to
		if err := os.WriteFile(filepath.Join(out, "model.gguf"), []byte(body), 0o600); err != nil {
			return 4
		}
		fmt.Print("PROGRESS 100 done") // no line break: read at the end
		return 0
	case "noisy":
		for i := 0; i < 2000; i++ {
			fmt.Println("a line of trainer output that fills the log")
		}
		return 0
	case "fail":
		fmt.Fprintln(os.Stderr, "boom")
		return 3
	case "sleep":
		fmt.Println("PROGRESS 10 waiting")
		time.Sleep(time.Minute)
		return 0
	case "stubborn": // ignores SIGTERM: only the kill after the grace period ends it
		signal.Ignore(syscall.SIGTERM)
		fmt.Println("PROGRESS 10 waiting")
		time.Sleep(time.Minute)
		return 0
	case "symlink":
		_ = os.Symlink(os.Getenv("MDDB_TRAIN_DATASET"), filepath.Join(out, "model.gguf"))
		return 0
	default: // "noout": success without a result file
		return 0
	}
}

// runFakeEvaluate writes a report as its mode says.
func runFakeEvaluate(mode string) int {
	out, model := os.Getenv("MDDB_EVAL_OUTPUT"), os.Getenv("MDDB_EVAL_MODEL")
	write := func(s string) int {
		if err := os.WriteFile(out, []byte(s), 0o600); err != nil { // #nosec G703 -- the report path the test server gave
			return 5
		}
		return 0
	}
	score := map[bool]float64{true: 0.4, false: 0.7}[model == ""]
	switch mode {
	case "eval-good":
		lines := countLines(os.Getenv("MDDB_EVAL_DATASET"))
		return write(fmt.Sprintf(`{"total":%d,"correct":1,"score":%g,"metrics":{"exact":%g},"samples":[{"input":"<b>i</b>","expected":"e","actual":"a","correct":false}]}`,
			max(lines, 1), score, score))
	case "eval-worse":
		return write(fmt.Sprintf(`{"total":2,"correct":1,"score":%g}`, 1-score))
	case "eval-bad":
		return write("not json")
	case "eval-big":
		return write(`{"total":1,"correct":1,"score":1,"pad":"` + strings.Repeat("x", maxEvalReportBytes) + `"}`)
	case "eval-exit":
		return 2
	case "eval-sleep":
		time.Sleep(time.Minute)
		return 0
	default: // "eval-escape": the report is a link to a file outside the job
		outside := filepath.Join(filepath.Dir(filepath.Dir(filepath.Dir(out))), "outside.json")
		_ = os.WriteFile(outside, []byte(`{"total":1,"correct":1,"score":1}`), 0o600) // #nosec G703 -- a test file beside the training root
		_ = os.Symlink(outside, out)
		return 0
	}
}

// countLines counts the lines of a file, 0 when it cannot be read.
func countLines(path string) int {
	f, err := os.Open(path) // #nosec G304 G703 -- the dataset the test server named
	if err != nil {
		return 0
	}
	defer func() { _ = f.Close() }()
	n := 0
	for sc := bufio.NewScanner(f); sc.Scan(); {
		n++
	}
	return n
}

// envNames is the sorted names of the environment.
func envNames() string {
	var names []string
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		names = append(names, name)
	}
	sort.Strings(names)
	return strings.Join(names, ",")
}

// trainingProfilesYAML are the fake's profiles; {{exe}} is the test binary.
const trainingProfilesYAML = `
trainers:
  - name: fake
    description: A test trainer
    command: '{{exe}}'
    args: ["train", "--data={dataset}", "--out={outputDir}"]
    progress: PROGRESS
    trialRecords: 2
    trialParameters: {epochs: 1}
    trialTimeout: 30s
    parameters:
      - {name: epochs, type: int, min: 1, max: 10, default: 3, args: ["--epochs", "{value}"]}
      - {name: rate, type: float, min: 0, max: 1, args: ["--rate={value}"]}
      - {name: method, type: enum, values: [a, b], default: a, args: ["--method", "{value}"]}
      - {name: verbose, type: bool, args: ["--verbose"]}
      - {name: tag, type: string, maxLength: 16, args: ["--tag", "{value}"]}
    evaluate:
      command: '{{exe}}'
      args: ["eval-good", "{evalModel}"]
      baseline: true
      parameters:
        - {name: limit, type: int, min: 1, default: 5, args: ["--limit={value}"]}
  - {name: worse, command: '{{exe}}', args: [train], evaluate: {command: '{{exe}}', args: [eval-worse], baseline: true}}
  - {name: nobase, command: '{{exe}}', args: [train], evaluate: {command: '{{exe}}', args: [eval-good]}}
  - {name: noeval, command: '{{exe}}', args: [train], maxConcurrent: 2}
  - {name: fail, command: '{{exe}}', args: [fail]}
  - {name: sleep, command: '{{exe}}', args: [sleep], progress: PROGRESS}
  - {name: stubborn, command: '{{exe}}', args: [stubborn], progress: PROGRESS}
  - {name: slow, command: '{{exe}}', args: [sleep], timeout: 1s}
  - {name: noout, command: '{{exe}}', args: [noout]}
  - {name: noisy, command: '{{exe}}', args: [noisy, "{jobId}"]}
  - {name: symlink, command: '{{exe}}', args: [symlink]}
  - {name: evalbad, command: '{{exe}}', args: [train], evaluate: {command: '{{exe}}', args: [eval-bad]}}
  - {name: evalbig, command: '{{exe}}', args: [train], evaluate: {command: '{{exe}}', args: [eval-big]}}
  - {name: evalexit, command: '{{exe}}', args: [train], evaluate: {command: '{{exe}}', args: [eval-exit]}}
  - {name: evalslow, command: '{{exe}}', args: [train], evaluate: {command: '{{exe}}', args: [eval-sleep], timeout: 1s}}
  - {name: evalescape, command: '{{exe}}', args: [train], evaluate: {command: '{{exe}}', args: [eval-escape]}}
  - {name: evalsleep, command: '{{exe}}', args: [train], evaluate: {command: '{{exe}}', args: [eval-sleep], baseline: true}}
`

// trainingFixture is a server with the fake's profiles and a training root.
type trainingFixture struct {
	s *Server
	m *trainingManager
}

// newTrainingFixture builds a server whose manager reads the fake's
// profiles; extra YAML profiles may be appended.
func newTrainingFixture(t *testing.T, extra string) *trainingFixture {
	t.Helper()
	s, cleanup := newHandlerTestServer(t)
	t.Cleanup(func() { s.closeCollectionFiles(); cleanup() })
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(t.TempDir(), "trainers.yaml")
	if err := os.WriteFile(cfg, []byte(strings.ReplaceAll(trainingProfilesYAML+extra, "{{exe}}", exe)), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(trainersConfigEnv, cfg)
	t.Setenv(trainingDirEnv, filepath.Join(t.TempDir(), "training"))
	grace := trainingGrace
	trainingGrace = 2 * time.Second
	t.Cleanup(func() { trainingGrace = grace })
	s.Training = newTrainingManager(s)
	t.Cleanup(s.Training.stop)
	return &trainingFixture{s: s, m: s.Training}
}

// seedRecipes adds two licensed and one unlicensed collections.
func (f *trainingFixture) seedRecipes(t *testing.T) {
	t.Helper()
	meta := func(c, d string) map[string][]string { return map[string][]string{"cuisine": {c}, "diet": {d}} }
	addTestDoc(t, f.s, "recipes", "soup", "en", "Boil the leeks.", meta("french", "vegan"))
	addTestDoc(t, f.s, "recipes", "stew", "en", "Simmer the beans.", meta("french", "vegan"))
	addTestDoc(t, f.s, "recipes", "pie", "en", "Bake the apples.", meta("english", "veg"))
	addTestDoc(t, f.s, "recipes", "dup", "en", "Boil the leeks.", meta("french", "vegan"))
	addTestDoc(t, f.s, "recipes", "tarte", "fr", "Cuire les pommes.", meta("french", "veg"))
	addTestDoc(t, f.s, "techniques", "braise", "en", "Brown, then cover and cook slowly.", nil)
	addTestDoc(t, f.s, "notes", "n1", "en", "An unlicensed note.", nil)
	for c, l := range map[string]string{"recipes": "CC-BY-4.0", "techniques": "CC0-1.0"} {
		if err := f.s.CollectionManager.put(c, &CollectionConfig{Corpus: &CorpusInfo{Name: c, License: l}}); err != nil {
			t.Fatal(err)
		}
	}
}

// wait polls a job until it ends.
func (f *trainingFixture) wait(t *testing.T, id string) *trainingJob {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		j, err := f.m.getJob(id)
		if err != nil {
			t.Fatal(err)
		}
		f.m.mu.Lock()
		_, live := f.m.running[id]
		f.m.mu.Unlock()
		if !j.active() && !live {
			return j
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("job %s did not end", id)
	return nil
}

// pairsRequest is the recipes as pairs, with a validation part.
func pairsRequest() datasetRequest {
	return datasetRequest{Collections: []string{"recipes"}, Format: datasetPairs, ValidationRatio: 0.5,
		Pairs: &pairMapping{Input: "A {meta.cuisine} dish, {meta.diet}", Output: "{content}", System: "You write recipes."}}
}
