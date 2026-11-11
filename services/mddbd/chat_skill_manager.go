package main

import (
	"sort"

	"mddb/internal/binlog"
	json "mddb/internal/jsonx"

	bolt "go.etcd.io/bbolt"
)

// The skill half of ChatScenarioManager (#404). Skills live in their own
// bucket of the main database, next to the scenarios, and share the
// scenarios' version counter: a skill that changes changes the prompt of
// every scenario using it, so the one number a consumer polls must move.

// bucketChatSkills holds one JSON value per skill, its history included.
var bucketChatSkills = []byte("chat_skills")

// loadChatSkills reads every skill of the bucket, skipping a corrupt entry.
func loadChatSkills(b *bolt.Bucket) map[string]*ChatSkill {
	m := map[string]*ChatSkill{}
	_ = b.ForEach(func(k, v []byte) error { // the callback never fails
		var sk ChatSkill
		if json.Unmarshal(v, &sk) == nil {
			m[string(k)] = &sk
		}
		return nil
	})
	return m
}

// GetSkill returns a copy of one skill. Safe on a nil manager.
func (c *ChatScenarioManager) GetSkill(name string) (*ChatSkill, bool) {
	if c == nil {
		return nil, false
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	sk, ok := c.skills[name]
	if !ok {
		return nil, false
	}
	cp := *sk
	return &cp, true
}

// ListSkills returns every skill, sorted by name.
func (c *ChatScenarioManager) ListSkills() []*ChatSkill {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]*ChatSkill, 0, len(c.skills))
	for _, sk := range c.skills {
		cp := *sk
		out = append(out, &cp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// SkillUsedBy names the scenarios that attach a skill, sorted.
func (c *ChatScenarioManager) SkillUsedBy(name string) []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.skillUsedBy(name)
}

// skillUsedBy is SkillUsedBy under the lock.
func (c *ChatScenarioManager) skillUsedBy(name string) []string {
	out := []string{}
	for _, sc := range c.m {
		for _, used := range sc.Skills {
			if used == name {
				out = append(out, sc.Name)
				break
			}
		}
	}
	sort.Strings(out)
	return out
}

// PutSkill creates a skill or stores a new version of it, already
// normalized by the caller: the version grows by one and the version before
// joins the history, which keeps the last chatSkillHistoryLen. A put that
// changes nothing stores nothing. It reports whether the skill is new, and
// fills sk with what is stored.
func (c *ChatScenarioManager) PutSkill(sk *ChatSkill) (created bool, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now().UTC()
	prev, exists := c.skills[sk.Name]
	if exists && prev.sameAs(sk) {
		*sk = *prev
		return false, nil
	}
	sk.Version, sk.History, sk.CreatedAt, sk.UpdatedAt = 1, nil, now, now
	if exists {
		sk.Version, sk.CreatedAt = prev.Version+1, prev.CreatedAt
		sk.History = append([]ChatSkillRevision{prev.revision()}, prev.History...)
		sk.History = sk.History[:min(len(sk.History), chatSkillHistoryLen)]
	}
	val, _ := json.Marshal(sk) // strings, numbers and times: it encodes
	if err := c.write(bucketChatSkills, func(b *bolt.Bucket) error { return b.Put([]byte(sk.Name), val) },
		&binlog.BinlogEntry{Type: binlog.BinlogPut, BucketName: string(bucketChatSkills), Key: []byte(sk.Name), Value: val}); err != nil {
		return false, err
	}
	cp := *sk
	c.skills[sk.Name] = &cp
	return !exists, nil
}

// DeleteSkill removes a skill no scenario attaches. While one does, it
// returns errChatSkillInUse with their names.
func (c *ChatScenarioManager) DeleteSkill(name string) (usedBy []string, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.skills[name]; !ok {
		return nil, errChatSkillNotFound
	}
	if usedBy = c.skillUsedBy(name); len(usedBy) > 0 {
		return usedBy, errChatSkillInUse
	}
	if err := c.write(bucketChatSkills, func(b *bolt.Bucket) error { return b.Delete([]byte(name)) },
		&binlog.BinlogEntry{Type: binlog.BinlogDelete, BucketName: string(bucketChatSkills), Key: []byte(name)}); err != nil {
		return nil, err
	}
	delete(c.skills, name)
	return nil, nil
}
