package main

import "time"

// FeatureState is one module as GET /v1/features, ListFeatures and
// feature_list show it (GO-049).
type FeatureState struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	// Enabled is whether the module works now: its switch is on, the
	// environment does not force it off, and every module it requires is on.
	Enabled bool `json:"enabled"`
	// Configured is its switch: what an administrator set, or the default.
	Configured bool     `json:"configured"`
	Default    bool     `json:"default"`
	Requires   []string `json:"requires"`
	// LockedBy names the environment variable that forces it off; such a
	// module cannot be switched (409).
	LockedBy string `json:"lockedBy,omitempty"`
	// BlockedBy lists the modules it requires that are off.
	BlockedBy []string   `json:"blockedBy,omitempty"`
	UpdatedAt *time.Time `json:"updatedAt,omitempty"`
	UpdatedBy string     `json:"updatedBy,omitempty"`
}

// featureList is the body of GET /v1/features.
type featureList struct {
	Features []FeatureState `json:"features"`
	Version  uint64         `json:"version"`
}

// Enabled reports whether a module works now. A name outside the registry is
// core and always enabled. Safe on a nil manager, which knows only defaults.
func (f *FeatureManager) Enabled(name string) bool {
	d, known := featureByName(name)
	if !known {
		return true
	}
	if f != nil {
		f.mu.RLock()
		defer f.mu.RUnlock()
	}
	return f.enabled(d)
}

// enabled is Enabled for a known module, under the read lock.
func (f *FeatureManager) enabled(d featureDef) bool {
	if f.forced(d.Name) || !f.setting(d).Enabled {
		return false
	}
	for _, req := range d.Requires {
		rd, _ := featureByName(req)
		if !f.enabled(rd) {
			return false
		}
	}
	return true
}

// State describes one module; false for a name outside the registry.
func (f *FeatureManager) State(name string) (FeatureState, bool) {
	d, known := featureByName(name)
	if !known {
		return FeatureState{}, false
	}
	if f != nil {
		f.mu.RLock()
		defer f.mu.RUnlock()
	}
	return f.state(d), true
}

// List describes every module, in the registry's order.
func (f *FeatureManager) List() []FeatureState {
	if f != nil {
		f.mu.RLock()
		defer f.mu.RUnlock()
	}
	out := make([]FeatureState, 0, len(featureRegistry))
	for _, d := range featureRegistry {
		out = append(out, f.state(d))
	}
	return out
}

// state builds a module's description, under the read lock.
func (f *FeatureManager) state(d featureDef) FeatureState {
	s := f.setting(d)
	st := FeatureState{Name: d.Name, Description: d.Description, Enabled: f.enabled(d), Configured: s.Enabled,
		Default: d.Default, Requires: append([]string{}, d.Requires...), UpdatedBy: s.UpdatedBy}
	if f.forced(d.Name) {
		st.LockedBy = featuresForceOffEnv
	}
	for _, req := range d.Requires {
		rd, _ := featureByName(req)
		if !f.enabled(rd) {
			st.BlockedBy = append(st.BlockedBy, req)
		}
	}
	if !s.UpdatedAt.IsZero() {
		at := s.UpdatedAt
		st.UpdatedAt = &at
	}
	return st
}
