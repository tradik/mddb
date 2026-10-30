package main

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"time"

	json "mddb/internal/jsonx"
	"mddb/internal/storage"
	proto "mddb/proto"
)

// POST /es/_bulk and /es/{index}/_bulk (INT-019). Runs of consecutive
// index/create operations on one collection and language are written by the
// batch path /v1/add-batch uses (processBatchWithDocs and its post-batch
// hooks: FTS, embeddings, webhooks); deletes go through
// deleteDocumentInternal. Operations keep their order: a run ends at a
// delete, at another collection or language, and at a second write of the
// same _id.
//
// The body is bounded by MDDB_MAX_BODY_BYTES like every request, and the
// operation count by esBulkMaxOps. An unsupported action or action option
// refuses the whole request before anything is written; a document that
// does not map, or that the write path refuses, fails only its own item.

// esBulkMaxOps bounds the operations in one _bulk request.
const esBulkMaxOps = 10000

// esBulkOp is one parsed operation.
type esBulkOp struct {
	Action string // index, create or delete
	Index  string
	ID     string
	Doc    *esDocument // index and create
	Err    error       // the source did not map; reported on the item
}

// parseESBulk reads an NDJSON bulk body.
func parseESBulk(body []byte, defaultIndex string, resolve func(string) string) ([]esBulkOp, error) {
	var lines [][]byte
	for _, l := range bytes.Split(body, []byte("\n")) {
		if l = bytes.TrimSpace(l); len(l) > 0 {
			lines = append(lines, l)
		}
	}
	var ops []esBulkOp
	for i := 0; i < len(lines); i++ {
		if len(ops) == esBulkMaxOps {
			return nil, dslErr("bulk", "more than %d operations in one request", esBulkMaxOps)
		}
		action, meta, err := esSingle(lines[i], "bulk")
		if err != nil {
			return nil, err
		}
		op, err := esBulkAction(action, meta, defaultIndex, resolve)
		if err != nil {
			return nil, err
		}
		if action != "delete" {
			if i++; i == len(lines) {
				return nil, dslErr("bulk."+action, "is missing its document line")
			}
			op.Doc, op.Err = esFromSource(lines[i])
		}
		ops = append(ops, op)
	}
	return ops, nil
}

// esBulkAction reads an action line's metadata.
func esBulkAction(action string, meta json.RawMessage, defaultIndex string, resolve func(string) string) (esBulkOp, error) {
	op := esBulkOp{Action: action, Index: defaultIndex}
	switch action {
	case "index", "create", "delete":
	default:
		return op, dslErr("bulk."+action, "action is not supported (index, create, delete)")
	}
	obj, err := esObject(meta, "bulk."+action)
	if err != nil {
		return op, err
	}
	if err := esOnlyKeys("bulk."+action, obj, "_index", "_id"); err != nil {
		return op, err
	}
	for key, dst := range map[string]*string{"_index": &op.Index, "_id": &op.ID} {
		if v, ok := obj[key]; ok && json.Unmarshal(v, dst) != nil {
			return op, dslErr("bulk."+action+"."+key, "must be a string")
		}
	}
	if op.Index == "" || strings.HasPrefix(op.Index, "_") {
		return op, dslErr("bulk."+action+"._index", "is missing or invalid")
	}
	op.Index = resolve(op.Index)
	if op.ID == "" {
		if action == "delete" {
			return op, dslErr("bulk.delete._id", "is required")
		}
		op.ID = esNewID()
	}
	return op, nil
}

// esBulkRun is a run of writes waiting for one batch.
type esBulkRun struct {
	index, lang string
	ops         []int
	keys        map[string]bool
}

// esBulk answers a bulk request.
func (s *Server) esBulk(w http.ResponseWriter, r *http.Request, defaultIndex string) {
	start := time.Now()
	if err := esWriteParamsError(r); err != nil {
		esFail(w, err)
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		esBodyError(w, err)
		return
	}
	if defaultIndex != "" && !s.permitted(w, r, defaultIndex, PermWrite) {
		return
	}
	ops, err := parseESBulk(body, defaultIndex, s.Aliases.Resolve)
	if err != nil {
		esFail(w, err)
		return
	}
	b := &esBulkBatch{s: s, r: r, ops: ops, items: make([]map[string]any, len(ops)), run: &esBulkRun{}}
	touched := map[string]bool{}
	for i, op := range ops {
		touched[op.Index] = true
		b.apply(i, op)
	}
	b.flush()
	failed := false
	for i, it := range b.items {
		failed = failed || it[ops[i].Action].(map[string]any)["error"] != nil
	}
	for index := range touched {
		s.esRefreshAfterWrite(r, index)
	}
	esJSON(w, http.StatusOK, map[string]any{"took": time.Since(start).Milliseconds(), "errors": failed, "items": b.items})
}

// esBulkBatch carries a bulk request's operations: consecutive index and
// create writes to one index and language gather in a run, written as one
// batch when something else comes between them.
type esBulkBatch struct {
	s     *Server
	r     *http.Request
	ops   []esBulkOp
	items []map[string]any
	run   *esBulkRun
}

// flush writes the run waiting for a batch and starts an empty one.
func (b *esBulkBatch) flush() {
	b.s.esBulkWrite(b.r.Context(), b.run, b.ops, b.items)
	b.run = &esBulkRun{}
}

// apply answers operation i, or queues it in the run.
func (b *esBulkBatch) apply(i int, op esBulkOp) {
	s, r := b.s, b.r
	switch {
	case op.Err != nil:
		b.items[i] = esBulkResult(op, 0, "", op.Err)
	case s.AuthManager != nil && s.AuthManager.CheckPermission(r.Context(), op.Index, PermWrite) != nil:
		b.items[i] = esBulkItem(op, http.StatusForbidden, "security_exception", "no write permission on "+op.Index)
	case op.Action == "delete":
		b.flush()
		status, result, err := s.esRemove(op.Index, op.ID, esLang(r))
		b.items[i] = esBulkResult(op, status, result, err)
	default:
		b.queue(i, op)
	}
}

// queue adds an index or create operation to the run, flushing first when
// it targets another index or language or repeats a key of the run. A create
// of a document that exists is answered with a conflict instead.
func (b *esBulkBatch) queue(i int, op esBulkOp) {
	lang := op.Doc.Lang
	if lang == "" {
		lang = esLang(b.r)
	}
	key := storage.NormalizeKey(op.ID)
	run := b.run
	if len(run.ops) > 0 && (run.index != op.Index || run.lang != lang || run.keys[key]) {
		b.flush()
		run = b.run
	}
	if op.Action == "create" {
		if _, err := b.s.loadDocByRef(op.Index, op.ID, lang); err == nil {
			b.items[i] = esBulkItem(op, http.StatusConflict, "version_conflict_engine_exception", "["+op.ID+"]: document already exists")
			return
		}
	}
	if len(run.ops) == 0 {
		run.index, run.lang, run.keys = op.Index, lang, map[string]bool{}
	}
	run.ops, run.keys[key] = append(run.ops, i), true
}

// esBulkWrite writes a run through the batch path and fills its items.
func (s *Server) esBulkWrite(ctx context.Context, run *esBulkRun, ops []esBulkOp, items []map[string]any) {
	if len(run.ops) == 0 {
		return
	}
	docs := make([]*proto.BatchDocument, len(run.ops))
	for j, i := range run.ops {
		d := ops[i].Doc
		docs[j] = &proto.BatchDocument{Key: ops[i].ID, Lang: run.lang, Meta: toProtoMeta(d.Meta), ContentMd: d.Content}
	}
	resp, processed, err := s.processBatchWithDocs(ctx, run.index, docs)
	if refusal := wholeBatchRefusal(err); refusal != nil {
		for _, i := range run.ops {
			items[i] = esBulkResult(ops[i], 0, "", refusal)
		}
		return
	}
	s.firePostBatchHooks(run.index, processed, postBatchOptions{})
	written := map[string]bool{}
	for _, p := range processed {
		written[storage.NormalizeKey(p.Doc.Key)] = p.IsUpdate
	}
	// Errors are "<key>: <reason>"; one that names no key of the run (a
	// failed transaction) is the reason for every document it left unwritten.
	reasons := map[string]string{}
	for _, i := range run.ops {
		reasons[storage.NormalizeKey(ops[i].ID)] = strings.Join(resp.Errors, "; ")
	}
	for _, e := range resp.Errors {
		key, reason, _ := strings.Cut(e, ": ")
		reasons[storage.NormalizeKey(key)] = reason
	}
	for _, i := range run.ops {
		key := storage.NormalizeKey(ops[i].ID)
		isUpdate, ok := written[key]
		switch {
		case !ok:
			items[i] = esBulkItem(ops[i], http.StatusBadRequest, "illegal_argument_exception", reasons[key])
		case isUpdate:
			items[i] = esBulkResult(ops[i], http.StatusOK, "updated", nil)
		default:
			items[i] = esBulkResult(ops[i], http.StatusCreated, "created", nil)
		}
	}
}

// esBulkItem is a failed item.
func esBulkItem(op esBulkOp, status int, typ, reason string) map[string]any {
	return map[string]any{op.Action: map[string]any{
		"_index": op.Index, "_id": op.ID, "status": status, "error": esErrorBody(typ, reason),
	}}
}

// esBulkResult is an item from a write's outcome.
func esBulkResult(op esBulkOp, status int, result string, err error) map[string]any {
	if err != nil {
		status, typ := esStatusOf(err)
		return esBulkItem(op, status, typ, err.Error())
	}
	return map[string]any{op.Action: esWriteResult(op.Index, op.ID, result, status)}
}
