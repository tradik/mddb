package cluster

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// The node's term and the vote it gave in it, on disk. A node that forgot
// either after a restart could vote twice in one term, and two candidates
// could both win it.

// vote is the persisted state.
type vote struct {
	Version  int    `json:"version"`
	Term     uint64 `json:"term"`
	VotedFor string `json:"votedFor,omitempty"`
	// Led is the last term this node led. A cluster without automatic
	// failover has no election to bring its leader back after a restart;
	// the node that led the current term takes it up again.
	Led uint64 `json:"led,omitempty"`
}

const voteVersion = 1

// Store keeps the term and vote in one small JSON file, replaced whole and
// synced before a vote is given or a term is taken.
type Store struct {
	path string
	// mu serializes saves: two saves sharing the temp file would replace
	// each other's half-written state.
	mu sync.Mutex
}

// NewStore is the store at path.
func NewStore(path string) *Store { return &Store{path: path} }

// Path is where the store is kept.
func (s *Store) Path() string { return s.path }

// load reads the state; a missing file is term 0 without a vote.
func (s *Store) load() (vote, error) {
	raw, err := os.ReadFile(filepath.Clean(s.path))
	if os.IsNotExist(err) {
		return vote{}, nil
	}
	if err != nil {
		return vote{}, err
	}
	var v vote
	if err := json.Unmarshal(raw, &v); err != nil {
		return vote{}, fmt.Errorf("%s is damaged: %w", s.path, err)
	}
	if v.Version > voteVersion {
		return vote{}, fmt.Errorf("%s was written by a newer release (version %d)", s.path, v.Version)
	}
	return v, nil
}

// save replaces the state.
func (s *Store) save(v vote) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	v.Version = voteVersion
	raw, _ := json.Marshal(v) // plain fields always encode
	tmp := s.path + ".tmp"
	f, err := os.OpenFile(filepath.Clean(tmp), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	_, err = f.Write(append(raw, '\n'))
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp, s.path)
	}
	if err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("saving the election term: %w", err)
	}
	return nil
}
