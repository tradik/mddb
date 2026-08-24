package schema

import (
	"bytes"
	"fmt"
	"mddb/internal/binlog"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"

	bolt "go.etcd.io/bbolt"
	json "mddb/internal/jsonx"
)

var bucketSchemas = []byte("schemas")

// SchemaManager manages per-collection JSON Schema definitions for metadata validation.
// Validation is opt-in: collections without a schema skip validation entirely.
type SchemaManager struct {
	db      *bolt.DB
	schemas map[string]*MetaSchema // collection → parsed schema
	mu      sync.RWMutex
	binlog  *binlog.Binlog
}

// SetBinlog sets the binlog for replication logging.
func (sm *SchemaManager) SetBinlog(bl *binlog.Binlog) {
	sm.binlog = bl
}

// MetaSchema is a parsed JSON Schema subset for metadata validation.
type MetaSchema struct {
	Raw        string                    `json:"_raw"`
	Required   []string                  `json:"required,omitempty"`
	Properties map[string]PropertySchema `json:"properties,omitempty"`
}

// PropertySchema defines validation rules for a single metadata key.
type PropertySchema struct {
	Type     string   `json:"type,omitempty"`     // string, number, integer, boolean
	Enum     []string `json:"enum,omitempty"`     // allowed values
	Pattern  string   `json:"pattern,omitempty"`  // regex pattern
	MinItems int      `json:"minItems,omitempty"` // min number of values
	MaxItems int      `json:"maxItems,omitempty"` // max number of values (0 = unlimited)
}

// NewSchemaManager creates a new schema manager.
func NewSchemaManager(db *bolt.DB) *SchemaManager {
	return &SchemaManager{
		db:      db,
		schemas: make(map[string]*MetaSchema),
	}
}

// Reload re-points the manager at a freshly restored database, drops the cached
// schemas and reloads them (GO-004). Keeping the same *SchemaManager (rather
// than swapping Server.SchemaManager) avoids racing the field with readers; the
// db handle and schema map are reset under the manager's own lock.
func (sm *SchemaManager) Reload(db *bolt.DB) error {
	sm.mu.Lock()
	sm.db = db
	sm.schemas = make(map[string]*MetaSchema)
	sm.mu.Unlock()
	if err := sm.EnsureBucket(); err != nil {
		return err
	}
	return sm.LoadAll()
}

// EnsureBucket creates the schemas bucket if it doesn't exist.
func (sm *SchemaManager) EnsureBucket() error {
	return sm.db.Update(func(tx *bolt.Tx) error {
		_, err := tx.CreateBucketIfNotExists(bucketSchemas)
		return err
	})
}

// LoadAll loads all schemas from BoltDB into memory.
func (sm *SchemaManager) LoadAll() error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	return sm.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketSchemas)
		if b == nil {
			return nil
		}
		return b.ForEach(func(k, v []byte) error {
			collection := strings.TrimPrefix(string(k), "schema|")
			schema, err := parseSchema(string(v))
			if err != nil {
				return fmt.Errorf("invalid schema for collection %q: %w", collection, err)
			}
			sm.schemas[collection] = schema
			return nil
		})
	})
}

// Set stores a JSON Schema for a collection. The schema is validated before saving.
func (sm *SchemaManager) Set(collection, schemaJSON string) error {
	schema, err := parseSchema(schemaJSON)
	if err != nil {
		return fmt.Errorf("invalid schema: %w", err)
	}

	key := []byte("schema|" + collection)
	val := []byte(schemaJSON)
	if err := sm.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketSchemas)
		return b.Put(key, val)
	}); err != nil {
		return err
	}

	if sm.binlog != nil {
		_ = sm.binlog.Append(&binlog.BinlogEntry{Type: binlog.BinlogPut, BucketName: "schemas", Key: bytes.Clone(key), Value: bytes.Clone(val)})
	}

	sm.mu.Lock()
	sm.schemas[collection] = schema
	sm.mu.Unlock()
	return nil
}

// Get returns the raw JSON Schema for a collection, or empty string if none.
func (sm *SchemaManager) Get(collection string) (string, bool) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	s, ok := sm.schemas[collection]
	if !ok {
		return "", false
	}
	return s.Raw, true
}

// Delete removes the schema for a collection (disables validation).
func (sm *SchemaManager) Delete(collection string) error {
	key := []byte("schema|" + collection)
	if err := sm.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketSchemas)
		return b.Delete(key)
	}); err != nil {
		return err
	}

	if sm.binlog != nil {
		_ = sm.binlog.Append(&binlog.BinlogEntry{Type: binlog.BinlogDelete, BucketName: "schemas", Key: bytes.Clone(key)})
	}

	sm.mu.Lock()
	delete(sm.schemas, collection)
	sm.mu.Unlock()
	return nil
}

// List returns all collections that have schemas defined.
func (sm *SchemaManager) List() map[string]string {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	result := make(map[string]string, len(sm.schemas))
	for col, s := range sm.schemas {
		result[col] = s.Raw
	}
	return result
}

// Validate checks metadata against the collection's schema.
// Returns nil if no schema is set (opt-in behavior).
//
// A nil receiver validates nothing rather than panicking. "No schema manager"
// and "no schema for this collection" mean the same thing to a caller — neither
// should reject the document — and half the call sites guarded the nil while
// half did not (TEST-002). Handling it here makes the whole class impossible
// instead of relying on every future caller remembering.
func (sm *SchemaManager) Validate(collection string, meta map[string][]string) error {
	if sm == nil {
		return nil
	}
	sm.mu.RLock()
	schema, ok := sm.schemas[collection]
	sm.mu.RUnlock()
	if !ok {
		return nil // no schema = no validation (opt-in)
	}
	return validateMeta(schema, meta)
}

// parseSchema parses and validates a JSON Schema string.
func parseSchema(raw string) (*MetaSchema, error) {
	var schema MetaSchema
	if err := json.Unmarshal([]byte(raw), &schema); err != nil {
		return nil, fmt.Errorf("invalid JSON: %w", err)
	}
	schema.Raw = raw

	// Validate patterns compile
	for key, prop := range schema.Properties {
		if prop.Pattern != "" {
			if _, err := regexp.Compile(prop.Pattern); err != nil {
				return nil, fmt.Errorf("invalid pattern for %q: %w", key, err)
			}
		}
		if prop.Type != "" {
			switch prop.Type {
			case "string", "number", "integer", "boolean":
			default:
				return nil, fmt.Errorf("unsupported type %q for %q (allowed: string, number, integer, boolean)", prop.Type, key)
			}
		}
		if prop.MinItems < 0 {
			return nil, fmt.Errorf("minItems for %q must be >= 0", key)
		}
		if prop.MaxItems < 0 {
			return nil, fmt.Errorf("maxItems for %q must be >= 0", key)
		}
	}
	return &schema, nil
}

// validateMeta validates metadata against a parsed schema.
func validateMeta(schema *MetaSchema, meta map[string][]string) error {
	var errs []string

	// Check required keys
	for _, key := range schema.Required {
		vals, ok := meta[key]
		if !ok || len(vals) == 0 {
			errs = append(errs, fmt.Sprintf("missing required field %q", key))
		}
	}

	// Check property rules
	for key, prop := range schema.Properties {
		vals, ok := meta[key]
		if !ok {
			continue // not required and not present = skip
		}

		// MinItems / MaxItems
		if prop.MinItems > 0 && len(vals) < prop.MinItems {
			errs = append(errs, fmt.Sprintf("field %q has %d values, minimum %d required", key, len(vals), prop.MinItems))
		}
		if prop.MaxItems > 0 && len(vals) > prop.MaxItems {
			errs = append(errs, fmt.Sprintf("field %q has %d values, maximum %d allowed", key, len(vals), prop.MaxItems))
		}

		for _, v := range vals {
			// Type validation
			if prop.Type != "" {
				if err := validateType(key, v, prop.Type); err != "" {
					errs = append(errs, err)
				}
			}
			// Enum validation
			if len(prop.Enum) > 0 {
				if !slices.Contains(prop.Enum, v) {
					errs = append(errs, fmt.Sprintf("field %q value %q not in allowed values %v", key, v, prop.Enum))
				}
			}
			// Pattern validation
			if prop.Pattern != "" {
				re := regexp.MustCompile(prop.Pattern) // already validated in parseSchema
				if !re.MatchString(v) {
					errs = append(errs, fmt.Sprintf("field %q value %q does not match pattern %q", key, v, prop.Pattern))
				}
			}
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("schema validation failed: %s", strings.Join(errs, "; "))
	}
	return nil
}

func validateType(key, value, expectedType string) string {
	switch expectedType {
	case "string":
		// all meta values are strings, always valid
		return ""
	case "number":
		if _, err := strconv.ParseFloat(value, 64); err != nil {
			return fmt.Sprintf("field %q value %q is not a valid number", key, value)
		}
	case "integer":
		if _, err := strconv.ParseInt(value, 10, 64); err != nil {
			return fmt.Sprintf("field %q value %q is not a valid integer", key, value)
		}
	case "boolean":
		if value != "true" && value != "false" {
			return fmt.Sprintf("field %q value %q is not a valid boolean (use \"true\" or \"false\")", key, value)
		}
	}
	return ""
}
