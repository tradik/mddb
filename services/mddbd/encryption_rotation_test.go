package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"mddb/internal/audit"
	"mddb/internal/encryption"
	"mddb/internal/storage"
	"strings"
	"testing"
	"time"

	bolt "go.etcd.io/bbolt"
)

// prevKeyEntry mirrors the unexported encryption.previousKey shape so tests can
// build the MDDB_ENCRYPTION_KEYS_PREVIOUS JSON without exporting that type.
type prevKeyEntry struct {
	ID  byte   `json:"id"`
	Key string `json:"key"`
}

// envBase64Key returns a base64-encoded random 32-byte key.
func envBase64Key(t *testing.T) string {
	t.Helper()
	return genKey(t) // reuse helper from encryption_glue_test.go
}

// withKey configures MDDB_ENCRYPTION_KEY plus optional ID and
// previous-keys env, then returns a fresh encryptor.
func withKey(t *testing.T, primary, id string, prev string) *encryption.Encryptor {
	t.Helper()
	t.Setenv("MDDB_ENCRYPTION_KEY", primary)
	t.Setenv("MDDB_ENCRYPTION_KEY_ID", id)
	t.Setenv("MDDB_ENCRYPTION_KEYS_PREVIOUS", prev)
	e, err := encryption.NewEncryptor()
	if err != nil {
		t.Fatalf("NewEncryptor: %v", err)
	}
	return e
}

// TestV2_RoundtripCarriesKeyID checks that ciphertext starts with the
// V2 magic and that the keyID byte matches the configured primary.
func TestV2_RoundtripCarriesKeyID(t *testing.T) {
	e := withKey(t, envBase64Key(t), "7", "")
	e.SetCollectionEnabled("c", true)
	ct, err := e.Encrypt([]byte("hello"), "c")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(ct, encryption.MagicV2) {
		t.Fatal("missing V2 magic")
	}
	if got := ct[encryption.MagicLen]; got != 7 {
		t.Fatalf("keyID byte = %d, want 7", got)
	}
	pt, err := e.Decrypt(ct)
	if err != nil || string(pt) != "hello" {
		t.Fatalf("decrypt: %q err=%v", pt, err)
	}
}

// TestV1_BackwardCompat reads a synthetic V1 ciphertext using the
// primary key — pre-2.9.16 deployments only ever had one key, so the
// upgrade path expects V1 to decrypt under the current primary.
func TestV1_BackwardCompat(t *testing.T) {
	keyB64 := envBase64Key(t)
	// Seal something with a "fake V1": same primary, but rewrite the
	// magic to V1 so the decrypt path takes the legacy branch.
	e := withKey(t, keyB64, "1", "")
	e.SetCollectionEnabled("c", true)
	v2, _ := e.Encrypt([]byte("legacy"), "c")
	// Strip the V2 keyID byte and replace magic with V1.
	v1 := append([]byte{}, encryption.MagicV1...)
	v1 = append(v1, v2[encryption.MagicLen+encryption.KeyIDLen:]...) // nonce + ct
	if !encryption.IsEncrypted(v1) {
		t.Fatal("V1 not detected as encrypted")
	}
	if got := encryption.CiphertextVersion(v1); got != 1 {
		t.Fatalf("version=%d, want 1", got)
	}
	pt, err := e.Decrypt(v1)
	if err != nil || string(pt) != "legacy" {
		t.Fatalf("V1 decrypt failed: %q err=%v", pt, err)
	}
}

// TestV2_PreviousKeyResolution writes under key 1, rotates to key 2
// (key 1 moved to PREVIOUS), and confirms the old ciphertext still
// decrypts. New writes go through key 2.
func TestV2_PreviousKeyResolution(t *testing.T) {
	k1 := envBase64Key(t)
	k2 := envBase64Key(t)

	e1 := withKey(t, k1, "1", "")
	e1.SetCollectionEnabled("c", true)
	old, err := e1.Encrypt([]byte("from-key-1"), "c")
	if err != nil {
		t.Fatal(err)
	}

	prev, _ := json.Marshal([]prevKeyEntry{{ID: 1, Key: k1}})
	e2 := withKey(t, k2, "2", string(prev))
	e2.SetCollectionEnabled("c", true)
	if e2.PrimaryKeyID() != 2 {
		t.Fatalf("primary=%d", e2.PrimaryKeyID())
	}
	if got, err := e2.Decrypt(old); err != nil || string(got) != "from-key-1" {
		t.Fatalf("legacy decrypt failed: %q err=%v", got, err)
	}
	new1, _ := e2.Encrypt([]byte("from-key-2"), "c")
	if new1[encryption.MagicLen] != 2 {
		t.Fatalf("new ciphertext keyID=%d, want 2", new1[encryption.MagicLen])
	}
	if got, _ := e2.Decrypt(new1); string(got) != "from-key-2" {
		t.Fatalf("roundtrip on new key failed: %q", got)
	}
}

// TestV2_UnknownKeyIDFails — an entry sealed with a key that the
// running process does not have configured must error rather than
// silently returning garbage.
func TestV2_UnknownKeyIDFails(t *testing.T) {
	k := envBase64Key(t)
	e := withKey(t, k, "1", "")
	e.SetCollectionEnabled("c", true)
	ct, _ := e.Encrypt([]byte("x"), "c")
	// Tamper the keyID byte to something we don't have.
	ct[encryption.MagicLen] = 99
	if _, err := e.Decrypt(ct); err == nil || !strings.Contains(err.Error(), "99") {
		t.Fatalf("expected unknown-keyID error, got %v", err)
	}
}

// TestPreviousKeysCannotCollide refuses to start when a previous key
// reuses the current primary keyID.
func TestPreviousKeysCannotCollide(t *testing.T) {
	k1 := envBase64Key(t)
	k2 := envBase64Key(t)
	prev, _ := json.Marshal([]prevKeyEntry{{ID: 2, Key: k1}})
	t.Setenv("MDDB_ENCRYPTION_KEY", k2)
	t.Setenv("MDDB_ENCRYPTION_KEY_ID", "2")
	t.Setenv("MDDB_ENCRYPTION_KEYS_PREVIOUS", string(prev))
	if _, err := encryption.NewEncryptor(); err == nil || !strings.Contains(err.Error(), "collides") {
		t.Fatalf("expected collision error, got %v", err)
	}
}

// TestPreviousKeyIDZeroRejected — keyID 0 is reserved (V1 implicit).
func TestPreviousKeyIDZeroRejected(t *testing.T) {
	k1 := envBase64Key(t)
	k2 := envBase64Key(t)
	prev, _ := json.Marshal([]prevKeyEntry{{ID: 0, Key: k1}})
	t.Setenv("MDDB_ENCRYPTION_KEY", k2)
	t.Setenv("MDDB_ENCRYPTION_KEY_ID", "1")
	t.Setenv("MDDB_ENCRYPTION_KEYS_PREVIOUS", string(prev))
	if _, err := encryption.NewEncryptor(); err == nil || !strings.Contains(err.Error(), "reserved") {
		t.Fatalf("expected keyID-0 reject, got %v", err)
	}
}

// TestKeyIDOutOfRange rejects MDDB_ENCRYPTION_KEY_ID outside 1..255.
func TestKeyIDOutOfRange(t *testing.T) {
	k := envBase64Key(t)
	t.Setenv("MDDB_ENCRYPTION_KEY", k)
	for _, bad := range []string{"0", "256", "-1", "abc"} {
		t.Setenv("MDDB_ENCRYPTION_KEY_ID", bad)
		if _, err := encryption.NewEncryptor(); err == nil {
			t.Errorf("KEY_ID=%q: expected error", bad)
		}
	}
}

// TestIsEncryptedWithPrimary correctly reports membership for V1, V2
// primary, V2 previous, and plaintext.
func TestIsEncryptedWithPrimary(t *testing.T) {
	k1 := envBase64Key(t)
	k2 := envBase64Key(t)
	prev, _ := json.Marshal([]prevKeyEntry{{ID: 1, Key: k1}})
	e := withKey(t, k2, "2", string(prev))
	e.SetCollectionEnabled("c", true)

	// Sealed with primary = true.
	curr, _ := e.Encrypt([]byte("now"), "c")
	if !e.IsEncryptedWithPrimary(curr) {
		t.Error("expected primary-match for current ciphertext")
	}

	// Forge a V2 with keyID=1 (previous).
	old, _ := e.Encrypt([]byte("old"), "c")
	old[encryption.MagicLen] = 1
	if e.IsEncryptedWithPrimary(old) {
		t.Error("ciphertext under previous key must not match primary")
	}

	if e.IsEncryptedWithPrimary([]byte("plaintext")) {
		t.Error("plaintext must not match primary")
	}
}

// TestRotation_EndToEnd seeds a database with documents sealed under
// old key 1, switches the encryptor to key 2 (with key 1 in PREVIOUS),
// runs a rotation job, and verifies every document is now sealed
// under key 2 and still decrypts to the original plaintext.
func TestRotation_EndToEnd(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	k1 := envBase64Key(t)
	k2 := envBase64Key(t)

	// Phase 1: seed with key 1.
	e1 := withKey(t, k1, "1", "")
	SetGlobalEncryptor(e1)
	defer SetGlobalEncryptor(nil)
	e1.SetCollectionEnabled("secrets", true)
	s.Encryptor = e1
	s.CollectionManager = NewCollectionManager(s.DB)
	if err := s.CollectionManager.EnsureBucket(); err != nil {
		t.Fatal(err)
	}
	if err := s.CollectionManager.Set("secrets", &CollectionConfig{Type: "default", Encrypted: true}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		addTestDoc(t, s, "secrets", string(rune('a'+i)), "en", "value-"+string(rune('a'+i)), nil)
	}

	// Phase 2: rotate to key 2.
	prev, _ := json.Marshal([]prevKeyEntry{{ID: 1, Key: k1}})
	e2 := withKey(t, k2, "2", string(prev))
	SetGlobalEncryptor(e2)
	e2.SetCollectionEnabled("secrets", true)
	s.Encryptor = e2
	s.RotationManager = NewRotationManager(s, e2)

	// Pre-rotation: every doc should still be readable through e2 via
	// previous keys.
	if err := assertDocsReadable(t, s, "secrets", 5); err != nil {
		t.Fatalf("pre-rotation read: %v", err)
	}

	// Run rotation synchronously (via Start + poll).
	job, err := s.RotationManager.Start(context.Background(), "secrets")
	if err != nil {
		t.Fatalf("rotation start: %v", err)
	}
	if !waitJob(t, s.RotationManager, job.ID, RotationCompleted, 5*time.Second) {
		final := s.RotationManager.Get(job.ID)
		t.Fatalf("rotation did not complete: %+v", final)
	}

	// Post-rotation: every docs entry must carry V2 magic with keyID=2.
	if err := s.DBViewC("secrets", func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte("docs"))
		return b.ForEach(func(k, v []byte) error {
			if collectionFromDocKey(k) != "secrets" {
				return nil
			}
			if encryption.CiphertextVersion(v) != 2 {
				t.Errorf("key=%s not V2", string(k))
				return nil
			}
			if v[encryption.MagicLen] != 2 {
				t.Errorf("key=%s sealed with id=%d, want 2", string(k), v[encryption.MagicLen])
			}
			return nil
		})
	}); err != nil {
		t.Fatal(err)
	}

	// And the plaintext is unchanged.
	if err := assertDocsReadable(t, s, "secrets", 5); err != nil {
		t.Fatalf("post-rotation read: %v", err)
	}
}

// TestRotation_StatusCounts verifies Status() classifies documents
// correctly across plaintext / primary / legacy buckets.
func TestRotation_StatusCounts(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	k1 := envBase64Key(t)
	k2 := envBase64Key(t)

	// Seed with key 1.
	e1 := withKey(t, k1, "1", "")
	SetGlobalEncryptor(e1)
	defer SetGlobalEncryptor(nil)
	e1.SetCollectionEnabled("secrets", true)
	s.Encryptor = e1
	s.CollectionManager = NewCollectionManager(s.DB)
	if err := s.CollectionManager.EnsureBucket(); err != nil {
		t.Fatal(err)
	}
	if err := s.CollectionManager.Set("secrets", &CollectionConfig{Type: "default", Encrypted: true}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		addTestDoc(t, s, "secrets", string(rune('a'+i)), "en", "v", nil)
	}

	// Switch to key 2 (key 1 in previous) and add 2 fresh docs.
	prev, _ := json.Marshal([]prevKeyEntry{{ID: 1, Key: k1}})
	e2 := withKey(t, k2, "2", string(prev))
	SetGlobalEncryptor(e2)
	e2.SetCollectionEnabled("secrets", true)
	s.Encryptor = e2
	s.RotationManager = NewRotationManager(s, e2)
	for i := 0; i < 2; i++ {
		addTestDoc(t, s, "secrets", string(rune('x'+i)), "en", "v", nil)
	}

	st, err := s.RotationManager.Status()
	if err != nil {
		t.Fatal(err)
	}
	if !st.Enabled || st.PrimaryKeyID != 2 {
		t.Fatalf("status header wrong: %+v", st)
	}
	var sec *CollectionStat
	for i := range st.Collections {
		if st.Collections[i].Collection == "secrets" {
			sec = &st.Collections[i]
		}
	}
	if sec == nil {
		t.Fatal("secrets collection missing from status")
	}
	if sec.WithPrimary != 2 || sec.WithLegacy != 3 {
		t.Errorf("counts: primary=%d legacy=%d total=%d", sec.WithPrimary, sec.WithLegacy, sec.Total)
	}
}

// TestRotation_DisabledEncryptor refuses to start a job.
func TestRotation_DisabledEncryptor(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	t.Setenv("MDDB_ENCRYPTION_KEY", "")
	e, _ := encryption.NewEncryptor()
	rm := NewRotationManager(s, e)
	if _, err := rm.Start(context.Background(), ""); err == nil {
		t.Fatal("expected error when encryptor disabled")
	}
}

// TestRotation_Reentrant returns the running job rather than queueing
// a second one.
func TestRotation_Reentrant(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	k := envBase64Key(t)
	e := withKey(t, k, "1", "")
	rm := NewRotationManager(s, e)
	// Manually inject a running job to avoid actually starting work.
	j := &RotationJob{ID: "rot-fake", Status: RotationRunning, StartedAt: time.Now().UnixNano()}
	rm.jobs[j.ID] = j
	rm.current = j
	got, err := rm.Start(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != j.ID {
		t.Fatalf("expected reuse of running job, got new id=%s", got.ID)
	}
}

// TestNewRotationID emits unique IDs.
func TestNewRotationID(t *testing.T) {
	a, b := newRotationID(), newRotationID()
	if a == b {
		t.Fatalf("ids collide: %s", a)
	}
	if !strings.HasPrefix(a, "rot-") {
		t.Errorf("missing prefix: %s", a)
	}
}

// TestCollectionFromDocKey parses keys correctly and rejects nonsense.
func TestCollectionFromDocKey(t *testing.T) {
	cases := map[string]string{
		"doc|blog|post1": "blog",
		"doc|c|x":        "c",
		"doc|":           "",
		"doc":            "",
		"rev|blog|x|0":   "",
		"":               "",
		"weird":          "",
	}
	for in, want := range cases {
		if got := collectionFromDocKey([]byte(in)); got != want {
			t.Errorf("%q: got %q, want %q", in, got, want)
		}
	}
}

// TestCollectionFromRevKey parses rev keys.
func TestCollectionFromRevKey(t *testing.T) {
	if got := collectionFromRevKey([]byte("rev|blog|x|0")); got != "blog" {
		t.Errorf("got %q", got)
	}
	if got := collectionFromRevKey([]byte("doc|blog|x")); got != "" {
		t.Errorf("doc key parsed as rev: %q", got)
	}
}

// --- helpers ---

func assertDocsReadable(t *testing.T, s *Server, coll string, want int) error {
	t.Helper()
	got := 0
	if err := s.DBViewC(coll, func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte("docs"))
		return b.ForEach(func(k, v []byte) error {
			if collectionFromDocKey(k) != coll {
				return nil
			}
			if _, err := loadDoc(v); err != nil {
				return err
			}
			got++
			return nil
		})
	}); err != nil {
		return err
	}
	if got != want {
		t.Errorf("readable docs: got %d, want %d", got, want)
	}
	return nil
}

func waitJob(t *testing.T, rm *RotationManager, id, want string, timeout time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		j := rm.Get(id)
		if j != nil && j.Status == want {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return false
}

// TestRotation_AuditEvents verifies rotation_started and
// rotation_completed are recorded when an audit.AuditManager is wired.
func TestRotation_AuditEvents(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	s.AuditManager = audit.NewAuditManager(s.DB, true, 1)
	if err := s.AuditManager.EnsureBuckets(); err != nil {
		t.Fatal(err)
	}
	s.AuditManager.Start()
	defer s.AuditManager.Stop()

	k := envBase64Key(t)
	e := withKey(t, k, "1", "")
	SetGlobalEncryptor(e)
	defer SetGlobalEncryptor(nil)
	s.Encryptor = e
	s.RotationManager = NewRotationManager(s, e)

	job, err := s.RotationManager.Start(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if !waitJob(t, s.RotationManager, job.ID, RotationCompleted, 3*time.Second) {
		t.Fatal("job never completed")
	}
	// Wait one writer flush cycle.
	time.Sleep(700 * time.Millisecond)
	events, err := s.AuditManager.Query(audit.QueryFilter{Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	var seenStarted, seenCompleted bool
	for _, ev := range events {
		switch ev.Action {
		case "encryption.rotation_started":
			seenStarted = true
		case "encryption.rotation_completed":
			seenCompleted = true
		}
	}
	if !seenStarted || !seenCompleted {
		t.Errorf("missing audit events: started=%v completed=%v", seenStarted, seenCompleted)
	}
}

// TestRotation_List_SortedNewestFirst checks sort order.
func TestRotation_List_SortedNewestFirst(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	k := envBase64Key(t)
	e := withKey(t, k, "1", "")
	rm := NewRotationManager(s, e)
	for i := 0; i < 3; i++ {
		j := &RotationJob{
			ID:        newRotationID(),
			Status:    RotationCompleted,
			StartedAt: time.Now().UnixNano(),
		}
		rm.jobs[j.ID] = j
		time.Sleep(time.Millisecond)
	}
	got := rm.List()
	if len(got) != 3 {
		t.Fatalf("got %d", len(got))
	}
	for i := 1; i < len(got); i++ {
		if got[i-1].StartedAt < got[i].StartedAt {
			t.Errorf("not sorted newest-first: %d < %d", got[i-1].StartedAt, got[i].StartedAt)
		}
	}
}

// TestProcessOne_DecryptError surfaces a corrupt entry as a job error.
func TestProcessOne_DecryptError(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	k := envBase64Key(t)
	e := withKey(t, k, "2", "")
	SetGlobalEncryptor(e)
	defer SetGlobalEncryptor(nil)
	s.Encryptor = e
	rm := NewRotationManager(s, e)

	// Inject a corrupt V2 ciphertext under an unknown keyID.
	bogus := append([]byte{}, encryption.MagicV2...)
	bogus = append(bogus, 99) // unknown keyID
	bogus = append(bogus, make([]byte, encryption.NonceLen+16)...)
	if err := s.DBUpdateC("c", func(tx *bolt.Tx) error {
		b, _ := tx.CreateBucketIfNotExists([]byte("docs"))
		return b.Put(storage.DocKey("c", "x"), bogus)
	}); err != nil {
		t.Fatal(err)
	}
	rewrote, err := rm.processOne("docs", storage.DocKey("c", "x"))
	if err == nil {
		t.Fatal("expected decrypt error")
	}
	if rewrote {
		t.Fatal("must not report rewrite on error path")
	}
}

// TestClassifyEntry_UnknownKey covers the fallback bucket.
func TestClassifyEntry_UnknownKey(t *testing.T) {
	cs := &CollectionStat{}
	// V2 magic but truncated before keyID byte.
	short := append([]byte{}, encryption.MagicV2...)
	classifyEntry(short, 1, cs)
	if cs.UnknownKey != 1 {
		t.Errorf("got %+v", cs)
	}
}

// TestStatus_NilManager returns Enabled=false safely.
func TestStatus_NilManager(t *testing.T) {
	var rm *RotationManager
	st, err := rm.Status()
	if err != nil {
		t.Fatal(err)
	}
	if st.Enabled {
		t.Error("nil manager should report disabled")
	}
}

// TestStartReturnsErrWhenManagerNil — nil receiver path.
func TestStartReturnsErrWhenManagerNil(t *testing.T) {
	var rm *RotationManager
	if _, err := rm.Start(context.Background(), ""); err == nil {
		t.Fatal("expected error on nil manager")
	}
}

// TestKeyBelongsToCollection returns true / false correctly.
func TestKeyBelongsToCollection(t *testing.T) {
	if !keyBelongsToCollection([]byte("doc|blog|x"), "blog") {
		t.Error("doc key should match")
	}
	if !keyBelongsToCollection([]byte("rev|blog|x|0"), "blog") {
		t.Error("rev key should match")
	}
	if keyBelongsToCollection([]byte("doc|other|x"), "blog") {
		t.Error("different collection must not match")
	}
	if keyBelongsToCollection([]byte("garbage"), "blog") {
		t.Error("garbage key must not match")
	}
}

// TestClassifyEntry_AllBuckets exercises every classifyEntry branch.
func TestClassifyEntry_AllBuckets(t *testing.T) {
	cs := &CollectionStat{}
	classifyEntry([]byte("plain"), 1, cs)
	if cs.Plaintext != 1 {
		t.Errorf("plaintext: %+v", cs)
	}

	v1 := append([]byte{}, encryption.MagicV1...)
	classifyEntry(v1, 1, cs)
	if cs.WithLegacy != 1 {
		t.Errorf("V1: %+v", cs)
	}

	v2primary := append(append([]byte{}, encryption.MagicV2...), 1)
	classifyEntry(v2primary, 1, cs)
	if cs.WithPrimary != 1 {
		t.Errorf("V2 primary: %+v", cs)
	}

	v2legacy := append(append([]byte{}, encryption.MagicV2...), 9)
	classifyEntry(v2legacy, 1, cs)
	if cs.WithLegacy != 2 {
		t.Errorf("V2 legacy: %+v", cs)
	}
}

// TestPreviousKeysParseError refuses obviously broken JSON.
func TestPreviousKeysParseError(t *testing.T) {
	k := envBase64Key(t)
	t.Setenv("MDDB_ENCRYPTION_KEY", k)
	t.Setenv("MDDB_ENCRYPTION_KEY_ID", "1")
	t.Setenv("MDDB_ENCRYPTION_KEYS_PREVIOUS", "not-json")
	if _, err := encryption.NewEncryptor(); err == nil {
		t.Fatal("expected JSON parse error")
	}
}

// TestPreviousKeysBadBase64 rejects an entry whose key is not 32 bytes.
func TestPreviousKeysBadBase64(t *testing.T) {
	k := envBase64Key(t)
	t.Setenv("MDDB_ENCRYPTION_KEY", k)
	t.Setenv("MDDB_ENCRYPTION_KEY_ID", "1")
	prev, _ := json.Marshal([]prevKeyEntry{{ID: 2, Key: base64.StdEncoding.EncodeToString([]byte("short"))}})
	t.Setenv("MDDB_ENCRYPTION_KEYS_PREVIOUS", string(prev))
	if _, err := encryption.NewEncryptor(); err == nil {
		t.Fatal("expected key-length error")
	}
}
