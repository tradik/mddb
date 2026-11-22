package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	json "mddb/internal/jsonx"
)

// The files of training jobs. Every job has a directory under the training
// root (MDDB_TRAINING_DIR), and every file MDDB writes or serves is reached
// through an os.Root on it, so no name — a job id, a result file a command
// left, a symbolic link it made — can lead outside. A file served or read
// back must be a regular file, not a link.

// Fixed names inside a job directory.
const (
	trainFile       = "train.jsonl"
	validationFile  = "validation.jsonl"
	datasetSubdir   = "dataset"
	trialSubdir     = "trial"
	outputSubdir    = "output"
	evalSubdir      = "eval"
	trainingLogName = "job.log"
)

var (
	// trainingDiskReserve is what an export leaves free on the training
	// root's file system.
	trainingDiskReserve uint64 = 256 << 20
	// trainingDiskSpace is diskSpace; a test replaces it to reach a failure.
	trainingDiskSpace = diskSpace

	// openInRoot is Root.Open; a test replaces it to swap the file between
	// the check and the open.
	openInRoot = (*os.Root).Open

	errTrainingDisk     = errors.New("not enough free disk space for the dataset")
	errTrainingNoResult = errors.New("no result file")
)

// openTrainingRoot opens the training root, creating it.
func (m *trainingManager) openTrainingRoot() (*os.Root, error) {
	if err := os.MkdirAll(m.root, 0o750); err != nil {
		return nil, err
	}
	return os.OpenRoot(m.root)
}

// jobRoot is an os.Root on a job's directory, created when create is set.
// The id is checked against the job id pattern before it names anything.
func (m *trainingManager) jobRoot(id string, create bool) (*os.Root, error) {
	if !trainingIDPattern.MatchString(id) {
		return nil, errTrainingNotFound
	}
	root, err := m.openTrainingRoot()
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	if create {
		_ = root.Mkdir(id, 0o750) // already there, or OpenRoot below says why not
	}
	return root.OpenRoot(id)
}

// jobPath is the absolute path of a name inside a job's directory, for the
// command's arguments and environment.
func (m *trainingManager) jobPath(id string, parts ...string) string {
	return filepath.Join(append([]string{m.root, id}, parts...)...)
}

// removeJobDir removes the directory of a job read from the store.
func (m *trainingManager) removeJobDir(id string) error {
	root, err := m.openTrainingRoot()
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	return root.RemoveAll(id)
}

// diskBudget is how many bytes an export may write: what is available
// beyond the reserve. Too little answers errTrainingDisk.
func (m *trainingManager) diskBudget() (int64, error) {
	var avail uint64
	err := os.MkdirAll(m.root, 0o750)
	if err == nil {
		avail, _, err = trainingDiskSpace(m.root)
	}
	if err != nil {
		return 0, err
	}
	if avail <= trainingDiskReserve {
		return 0, fmt.Errorf("%w: %d bytes available, %d kept free", errTrainingDisk, avail, trainingDiskReserve)
	}
	return int64(min(avail-trainingDiskReserve, 1<<62)), nil
}

// writeRecords writes records as JSON Lines to name in dir, failing once
// the budget is spent; it returns the bytes written.
func writeRecords(dir *os.Root, name string, records []datasetRecord, budget int64) (int64, error) {
	f, err := dir.OpenFile(name, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o640)
	if err != nil {
		return 0, err
	}
	bw := bufio.NewWriter(f)
	var n int64
	for i := range records {
		b, _ := json.Marshal(&records[i]) // strings only: it cannot fail
		if n += int64(len(b)) + 1; n > budget {
			_ = f.Close()
			return n, errTrainingDisk
		}
		_, _ = bw.Write(append(b, '\n'))
	}
	return n, errors.Join(bw.Flush(), f.Close())
}

// cutRecords copies the first n lines of src to dst, for a trial run; it
// returns how many it copied.
func cutRecords(src, dst *os.Root, srcName, dstName string, n int) (int, error) {
	in, err := src.Open(srcName)
	if err != nil {
		return 0, err
	}
	defer func() { _ = in.Close() }()
	out, err := dst.OpenFile(dstName, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o640)
	if err != nil {
		return 0, err
	}
	r, w := bufio.NewReader(in), bufio.NewWriter(out)
	copied := 0
	for copied < n {
		line, err := r.ReadBytes('\n')
		if len(line) > 0 {
			_, _ = w.Write(line)
			copied++
		}
		if err != nil {
			break // io.EOF: the source had fewer lines
		}
	}
	return copied, errors.Join(w.Flush(), out.Close())
}

// openRegular opens name in dir when it is a regular file — not a link,
// not a directory — and returns it with its size. The file opened must be
// the one examined, so a link put in its place meanwhile is refused too.
func openRegular(dir *os.Root, name string) (*os.File, int64, error) {
	info, err := dir.Lstat(name)
	if err == nil && !info.Mode().IsRegular() {
		err = fmt.Errorf("%s is not a regular file", name)
	}
	if err != nil {
		return nil, 0, err
	}
	f, err := openInRoot(dir, name)
	if err != nil {
		return nil, 0, err
	}
	if opened, err := f.Stat(); err != nil || !os.SameFile(info, opened) {
		_ = f.Close()
		return nil, 0, fmt.Errorf("%s changed while it was opened", name)
	}
	return f, info.Size(), nil
}

// findResult is the first regular file in a job's output directory whose
// name matches the profile's pattern. The listing goes through the job's
// root, so an output directory replaced by a link out of it is refused.
func findResult(job *os.Root, pattern string) (*trainingResult, error) {
	entries, err := fs.ReadDir(job.FS(), outputSubdir)
	if err != nil {
		return nil, err
	}
	var found []fs.DirEntry
	for _, e := range entries {
		if ok, _ := filepath.Match(pattern, e.Name()); ok && e.Type().IsRegular() {
			found = append(found, e)
		}
	}
	if len(found) == 0 {
		return nil, fmt.Errorf("%w: the command wrote no regular file matching %q in its output directory", errTrainingNoResult, pattern)
	}
	first := found[0] // ReadDir sorts by name
	res := &trainingResult{File: first.Name()}
	if info, err := first.Info(); err == nil {
		res.Bytes = info.Size()
	}
	return res, nil
}

// openResult opens a run's result for download: a regular file in its
// output directory whose name still matches the stored pattern.
func (m *trainingManager) openResult(j *trainingJob) (*os.File, int64, error) {
	if j.Result == nil {
		return nil, 0, errTrainingNoResult
	}
	if ok, _ := filepath.Match(j.OutputPattern, j.Result.File); !ok {
		return nil, 0, errTrainingNoResult
	}
	return m.openJobFile(j.ID, outputSubdir+"/"+j.Result.File)
}

// openJobFile opens a file of a job (a dataset part, a report, a result).
func (m *trainingManager) openJobFile(id, name string) (*os.File, int64, error) {
	jr, err := m.jobRoot(id, false)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = jr.Close() }()
	return openRegular(jr, name)
}

// logTail is the end of a job's log.
func (m *trainingManager) logTail(id string) string {
	f, size, err := m.openJobFile(id, trainingLogName)
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }()
	if size > int64(trainingLogTail) {
		_, _ = f.Seek(size-int64(trainingLogTail), io.SeekStart)
	}
	b, _ := io.ReadAll(io.LimitReader(f, int64(trainingLogTail)))
	return string(b)
}
