package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"mddb/internal/automationlog"
	"mddb/internal/httpclient"
	json "mddb/internal/jsonx"
	"mddb/internal/sentiment"
	"mddb/internal/storage"
	"net/http"
	"time"
)

// TriggerMatch represents a document that matched a trigger's search criteria.
type TriggerMatch struct {
	DocID      string  `json:"docId"`
	Key        string  `json:"key,omitempty"`
	Collection string  `json:"collection"`
	Score      float64 `json:"score"`
}

// TriggerPayload is sent to webhook URLs when a trigger fires.
type TriggerPayload struct {
	Event          string                `json:"event"` // "trigger.matched"
	Trigger        TriggerPayloadTrigger `json:"trigger"`
	Collection     string                `json:"collection"`
	Document       *storage.Doc          `json:"document,omitempty"`
	Score          float64               `json:"score"`
	SentimentScore float64               `json:"sentimentScore,omitempty"`
	Timestamp      int64                 `json:"timestamp"`
}

// TriggerPayloadTrigger is the trigger info in the payload.
type TriggerPayloadTrigger struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// EvaluateTriggers checks all enabled triggers for a collection matching the given event.
// event is one of "insert", "update", "delete" (MySQL-style).
// Called asynchronously from addDocument() and deleteDocumentInternal().
func (am *AutomationManager) EvaluateTriggers(collection string, doc storage.Doc, event string) {
	triggers := am.EnabledTriggersForEvent(collection, event)
	if len(triggers) == 0 {
		return
	}

	for _, trigger := range triggers {
		trigger := trigger // capture
		am.evaluateSingleTrigger(&trigger, &doc)
	}
}

// evaluateSingleTrigger runs a trigger's conditions and fires webhook if the doc matches.
// Supports search conditions (FTS/vector/hybrid), sentiment conditions, or both (AND/OR).
// If no conditions are set, fires unconditionally.
func (am *AutomationManager) evaluateSingleTrigger(trigger *AutomationRule, doc *storage.Doc) {
	if am.server == nil {
		return
	}

	hasSearch := trigger.Query != ""
	hasSentiment := trigger.SentimentEnabled
	conditionLogic := trigger.ConditionLogic
	if conditionLogic == "" {
		conditionLogic = "and"
	}

	var searchScore float64
	var searchMatched bool
	var sentimentScore float64
	var sentimentMatched bool

	// Evaluate search condition
	if hasSearch {
		switch trigger.SearchType {
		case "fts":
			searchScore, searchMatched = am.evalFTS(trigger, doc)
		case "vector":
			searchScore, searchMatched = am.evalVector(trigger, doc)
		case "hybrid":
			searchScore, searchMatched = am.evalHybrid(trigger, doc)
		}
	}

	// Evaluate sentiment condition
	if hasSentiment {
		sentimentScore = sentiment.AnalyzeSentiment(doc.ContentMD)
		sentimentMatched = sentimentScore >= trigger.SentimentMin && sentimentScore <= trigger.SentimentMax
	}

	// Determine overall match
	var matched bool
	if !hasSearch && !hasSentiment {
		matched = true
	} else if hasSearch && hasSentiment {
		if conditionLogic == "or" {
			matched = searchMatched || sentimentMatched
		} else {
			matched = searchMatched && sentimentMatched
		}
	} else if hasSearch {
		matched = searchMatched
	} else {
		matched = sentimentMatched
	}

	if !matched {
		return
	}

	// Use search score if available, otherwise sentiment score
	score := searchScore
	if !hasSearch {
		score = sentimentScore
	}

	// Track trigger fire
	if am.server != nil && am.server.Metrics != nil {
		searchType := trigger.SearchType
		if searchType == "" && hasSentiment {
			searchType = "sentiment"
		}
		am.server.Metrics.IncOp("automation_trigger", searchType)
	}

	// Resolve webhook
	webhook := am.GetWebhook(trigger.WebhookID)
	if webhook == nil || !webhook.Enabled {
		if am.logStore != nil {
			_ = am.logStore.Log(automationlog.Entry{
				Timestamp: time.Now().Unix(),
				RuleID:    trigger.ID,
				RuleName:  trigger.Name,
				RuleType:  "trigger",
				WebhookID: trigger.WebhookID,
				Status:    "skipped",
				Error:     "webhook not found or disabled",
			})
		}
		return
	}

	go fireAutomationWebhook(webhook, trigger, doc, trigger.Collection, score, sentimentScore, am.logStore)
}

// evalFTS runs FTS search and checks if doc appears in results above threshold.
func (am *AutomationManager) evalFTS(trigger *AutomationRule, doc *storage.Doc) (float64, bool) {
	s := am.server
	if s.FTSIndex == nil {
		return 0, false
	}

	results, err := s.FTSIndex.Search(trigger.Collection, trigger.Query, 100)
	if err != nil {
		slog.Warn("trigger FTS search error", "iD", trigger.ID, "err", err) // #nosec G706 -- internal log
		return 0, false
	}

	for _, r := range results {
		if r.DocID == doc.ID {
			// FTS threshold: raw BM25 score
			if r.Score >= trigger.Threshold {
				return r.Score, true
			}
			return r.Score, false
		}
	}
	return 0, false
}

// evalVector runs vector search and checks if doc appears above threshold.
func (am *AutomationManager) evalVector(trigger *AutomationRule, doc *storage.Doc) (float64, bool) {
	s := am.server
	if s.Embedding == nil {
		return 0, false
	}

	searcher, ok := s.VectorSearchers["flat"]
	if !ok || !searcher.IsReady() {
		return 0, false
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	queryVector, err := s.Embedding.Embed(ctx, trigger.Query)
	if err != nil {
		slog.Warn("trigger embedding error", "iD", trigger.ID, "err", err) // #nosec G706 -- internal log
		return 0, false
	}

	// Vector threshold: 0-100 maps to 0-1 similarity
	threshold := trigger.Threshold / 100.0
	results := searcher.Search(trigger.Collection, queryVector, 100, float64(threshold), nil)

	for _, r := range results {
		if r.DocID == doc.ID {
			score := float64(r.Score) * 100 // normalize to 0-100
			return score, true
		}
	}
	return 0, false
}

// evalHybrid runs hybrid search and checks if doc appears above threshold.
func (am *AutomationManager) evalHybrid(trigger *AutomationRule, doc *storage.Doc) (float64, bool) {
	s := am.server

	// Build a hybrid search request from trigger params
	req := HybridSearchRequest{
		Collection: trigger.Collection,
		Query:      trigger.Query,
		TopK:       100,
		Alpha:      floatPtr(0.5),
		Strategy:   "alpha",
	}

	// Override from searchParams if provided
	if sp := trigger.SearchParams; sp != nil {
		if v, ok := sp["alpha"].(float64); ok {
			req.Alpha = floatPtr(v)
		}
		if v, ok := sp["strategy"].(string); ok {
			req.Strategy = v
		}
		if v, ok := sp["algorithm"].(string); ok {
			req.Algorithm = v
		}
		if v, ok := sp["vectorAlgorithm"].(string); ok {
			req.VectorAlgorithm = v
		}
	}

	if req.Algorithm == "" {
		req.Algorithm = "bm25"
	}
	if req.VectorAlgorithm == "" {
		req.VectorAlgorithm = "flat"
	}

	// Run FTS
	ftsResults, err := s.runFTSSearch(req)
	if err != nil {
		slog.Warn("trigger hybrid FTS error", "iD", trigger.ID, "err", err) // #nosec G706 -- internal log
	}

	// Run vector
	ctx := context.Background()
	vectorResults, err := s.runVectorSearch(ctx, req)
	if err != nil {
		slog.Warn("trigger hybrid vector error", "iD", trigger.ID, "err", err) // #nosec G706 -- internal log
	}

	// Merge
	var merged []HybridSearchResultItem
	switch req.Strategy {
	case "rrf":
		merged = mergeRRF(ftsResults, vectorResults, req.RRFK, req.TopK)
	default:
		merged = mergeAlpha(ftsResults, vectorResults, alphaOrDefault(req.Alpha), req.TopK)
	}

	for _, m := range merged {
		if m.Document.ID == doc.ID {
			score := m.CombinedScore * 100 // normalize to 0-100
			if score >= trigger.Threshold {
				return score, true
			}
			return score, false
		}
	}
	return 0, false
}

// RunTrigger executes a trigger's search and returns all matches above threshold.
// Used by cron scheduler and manual test endpoint.
// ErrUnknownSearchType reports a trigger configured with a search type MDDB
// does not implement.
//
// This used to return (nil, nil): no matches and no error, indistinguishable
// from a trigger that simply matched nothing. An operator who mistyped
// "hybrid" would see a rule that never fires and no reason why (TEST-002).
var ErrUnknownSearchType = errors.New("unknown trigger search type")

func (am *AutomationManager) RunTrigger(trigger *AutomationRule) ([]TriggerMatch, error) {
	if am.server == nil {
		return nil, nil
	}

	switch trigger.SearchType {
	case "fts":
		return am.runTriggerFTS(trigger)
	case "vector":
		return am.runTriggerVector(trigger)
	case "hybrid":
		return am.runTriggerHybrid(trigger)
	default:
		return nil, fmt.Errorf("%w: %q (want fts, vector or hybrid)", ErrUnknownSearchType, trigger.SearchType)
	}
}

func (am *AutomationManager) runTriggerFTS(trigger *AutomationRule) ([]TriggerMatch, error) {
	s := am.server
	if s.FTSIndex == nil {
		return nil, nil
	}

	results, err := s.FTSIndex.Search(trigger.Collection, trigger.Query, 100)
	if err != nil {
		return nil, err
	}

	var matches []TriggerMatch
	for _, r := range results {
		if r.Score >= trigger.Threshold {
			matches = append(matches, TriggerMatch{
				DocID:      r.DocID,
				Collection: trigger.Collection,
				Score:      r.Score,
			})
		}
	}
	return matches, nil
}

func (am *AutomationManager) runTriggerVector(trigger *AutomationRule) ([]TriggerMatch, error) {
	s := am.server
	if s.Embedding == nil {
		return nil, nil
	}

	searcher, ok := s.VectorSearchers["flat"]
	if !ok || !searcher.IsReady() {
		return nil, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	queryVector, err := s.Embedding.Embed(ctx, trigger.Query)
	if err != nil {
		return nil, err
	}

	threshold := trigger.Threshold / 100.0
	results := searcher.Search(trigger.Collection, queryVector, 100, float64(threshold), nil)

	var matches []TriggerMatch
	for _, r := range results {
		score := float64(r.Score) * 100
		matches = append(matches, TriggerMatch{
			DocID:      r.DocID,
			Collection: trigger.Collection,
			Score:      score,
		})
	}
	return matches, nil
}

func (am *AutomationManager) runTriggerHybrid(trigger *AutomationRule) ([]TriggerMatch, error) {
	s := am.server

	req := HybridSearchRequest{
		Collection:      trigger.Collection,
		Query:           trigger.Query,
		TopK:            100,
		Alpha:           floatPtr(0.5),
		Strategy:        "alpha",
		Algorithm:       "bm25",
		VectorAlgorithm: "flat",
	}

	if sp := trigger.SearchParams; sp != nil {
		if v, ok := sp["alpha"].(float64); ok {
			req.Alpha = floatPtr(v)
		}
		if v, ok := sp["strategy"].(string); ok {
			req.Strategy = v
		}
	}

	ftsResults, _ := s.runFTSSearch(req)
	ctx := context.Background()
	vectorResults, _ := s.runVectorSearch(ctx, req)

	var merged []HybridSearchResultItem
	switch req.Strategy {
	case "rrf":
		merged = mergeRRF(ftsResults, vectorResults, req.RRFK, req.TopK)
	default:
		merged = mergeAlpha(ftsResults, vectorResults, alphaOrDefault(req.Alpha), req.TopK)
	}

	var matches []TriggerMatch
	for _, m := range merged {
		score := m.CombinedScore * 100
		if score >= trigger.Threshold {
			matches = append(matches, TriggerMatch{
				DocID:      m.Document.ID,
				Collection: trigger.Collection,
				Score:      score,
			})
		}
	}
	return matches, nil
}

// RunTriggerAndFire executes a trigger and fires the webhook for each match.
// Used by the cron scheduler.
func (am *AutomationManager) RunTriggerAndFire(trigger *AutomationRule) {
	webhook := am.GetWebhook(trigger.WebhookID)
	if webhook == nil || !webhook.Enabled {
		slog.Info("trigger webhook not found or disabled", "iD", trigger.ID, "webhookID", trigger.WebhookID)
		return
	}

	matches, err := am.RunTrigger(trigger)
	if err != nil {
		slog.Warn("trigger search error", "iD", trigger.ID, "err", err)
		return
	}

	for _, match := range matches {
		go fireAutomationWebhook(webhook, trigger, nil, match.Collection, match.Score, 0, am.logStore)
	}

	if len(matches) > 0 {
		slog.Info("trigger fired webhook matches", "iD", trigger.ID, "iD2", webhook.ID, "matchesCount", len(matches))
	}
}

// CronPayload is sent to webhook URLs when a cron fires.
type CronPayload struct {
	Event     string          `json:"event"` // "cron.fired"
	Cron      CronPayloadCron `json:"cron"`
	Timestamp int64           `json:"timestamp"`
}

// CronPayloadCron is the cron info in the payload.
type CronPayloadCron struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// fireCronWebhook sends a cron payload to a webhook URL.
func fireCronWebhook(webhook *AutomationRule, cronID, cronName string, logStore *automationlog.Store) {
	start := time.Now()

	payload := CronPayload{
		Event: "cron.fired",
		Cron: CronPayloadCron{
			ID:   cronID,
			Name: cronName,
		},
		Timestamp: start.Unix(),
	}

	data, err := json.Marshal(payload)
	if err != nil {
		slog.Warn("cron marshal error", "cronID", cronID, "err", err)
		return
	}

	method := webhook.Method
	if method == "" {
		method = "POST"
	}

	// Expand template variables in webhook URL and headers
	cronVars := BuildCronVars(webhook, cronID, cronName)
	expandedURL, expandedHeaders := expandWebhookURLAndHeaders(webhook.URL, webhook.Headers, cronVars)

	var finalStatus string
	var lastHTTPStatus int
	var lastError string
	var lastAttempt int

	backoffs := []time.Duration{0, 1 * time.Second, 5 * time.Second, 15 * time.Second}
	for attempt, backoff := range backoffs {
		if backoff > 0 {
			time.Sleep(backoff)
		}
		lastAttempt = attempt + 1

		req, err := http.NewRequest(method, expandedURL, bytes.NewReader(data))
		if err != nil {
			slog.Warn("cron webhook request error", "cronID", cronID, "iD", webhook.ID, "err", err)
			lastError = err.Error()
			finalStatus = "error"
			break
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-MDDB-Event", "cron.fired")
		req.Header.Set("X-MDDB-Cron-ID", cronID)
		req.Header.Set("X-MDDB-Webhook-ID", webhook.ID)

		for k, v := range expandedHeaders {
			req.Header.Set(k, v)
		}

		resp, err := httpclient.NewPooledClientWithTimeout(10 * time.Second).Do(req)
		if err != nil {
			slog.Warn("cron webhook attempt failed", "cronID", cronID, "iD", webhook.ID, "attempt", attempt+1, "err", err)
			lastError = err.Error()
			finalStatus = "error"
			continue
		}
		lastHTTPStatus = resp.StatusCode
		httpclient.DrainAndClose(resp.Body)

		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			finalStatus = "success"
			lastError = ""
			break
		}
		slog.Info("cron webhook attempt returned an error status", "cronID", cronID, "iD", webhook.ID, "attempt", attempt+1, "statusCode", resp.StatusCode)
		lastError = fmt.Sprintf("HTTP %d", resp.StatusCode)
		finalStatus = "error"
	}

	if finalStatus == "" {
		finalStatus = "error"
	}
	if finalStatus == "error" {
		slog.Info("cron webhook all retries exhausted", "cronID", cronID, "iD", webhook.ID)
	}

	if logStore != nil {
		_ = logStore.Log(automationlog.Entry{
			Timestamp:  start.Unix(),
			RuleID:     cronID,
			RuleName:   cronName,
			RuleType:   "cron",
			WebhookID:  webhook.ID,
			WebhookURL: expandedURL,
			Status:     finalStatus,
			HTTPStatus: lastHTTPStatus,
			DurationMs: time.Since(start).Milliseconds(),
			Error:      lastError,
			Attempt:    lastAttempt,
		})
	}
}

// fireAutomationWebhook sends the trigger payload to a webhook URL.
func fireAutomationWebhook(webhook *AutomationRule, trigger *AutomationRule, doc *storage.Doc, collection string, score float64, sentimentScore float64, logStore *automationlog.Store) {
	start := time.Now()

	payload := TriggerPayload{
		Event: "trigger.matched",
		Trigger: TriggerPayloadTrigger{
			ID:   trigger.ID,
			Name: trigger.Name,
		},
		Collection:     collection,
		Document:       doc,
		Score:          score,
		SentimentScore: sentimentScore,
		Timestamp:      start.Unix(),
	}

	data, err := json.Marshal(payload)
	if err != nil {
		slog.Warn("trigger marshal error", "iD", trigger.ID, "err", err) // #nosec G706 -- internal log
		return
	}

	method := webhook.Method
	if method == "" {
		method = "POST"
	}

	// Expand template variables in webhook URL and headers
	triggerVars := BuildTriggerVars(webhook, trigger, doc, collection, score, sentimentScore)
	expandedURL, expandedHeaders := expandWebhookURLAndHeaders(webhook.URL, webhook.Headers, triggerVars)

	var finalStatus string
	var lastHTTPStatus int
	var lastError string
	var lastAttempt int

	backoffs := []time.Duration{0, 1 * time.Second, 5 * time.Second, 15 * time.Second}
	for attempt, backoff := range backoffs {
		if backoff > 0 {
			time.Sleep(backoff)
		}
		lastAttempt = attempt + 1

		req, err := http.NewRequest(method, expandedURL, bytes.NewReader(data)) // #nosec G704 -- URL from internal webhook config
		if err != nil {
			slog.Warn("trigger webhook request error", "iD", trigger.ID, "iD2", webhook.ID, "err", err) // #nosec G706 -- internal log
			lastError = err.Error()
			finalStatus = "error"
			break
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-MDDB-Event", "trigger.matched")
		req.Header.Set("X-MDDB-Trigger-ID", trigger.ID)
		req.Header.Set("X-MDDB-Webhook-ID", webhook.ID)

		for k, v := range expandedHeaders {
			req.Header.Set(k, v)
		}

		resp, err := httpclient.NewPooledClientWithTimeout(10 * time.Second).Do(req) // #nosec G704 -- URL from internal webhook config
		if err != nil {
			slog.Warn("trigger webhook attempt failed", "iD", trigger.ID, "iD2", webhook.ID, "attempt", attempt+1, "err", err) // #nosec G706 -- internal log
			lastError = err.Error()
			finalStatus = "error"
			continue
		}
		lastHTTPStatus = resp.StatusCode
		httpclient.DrainAndClose(resp.Body)

		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			finalStatus = "success"
			lastError = ""
			break
		}
		slog.Info("trigger webhook attempt returned an error status", "iD", trigger.ID, "iD2", webhook.ID, "attempt", attempt+1, "statusCode", resp.StatusCode) // #nosec G706 -- internal log
		lastError = fmt.Sprintf("HTTP %d", resp.StatusCode)
		finalStatus = "error"
	}

	if finalStatus == "" {
		finalStatus = "error"
	}
	if finalStatus == "error" {
		slog.Info("trigger webhook all retries exhausted", "iD", trigger.ID, "iD2", webhook.ID) // #nosec G706 -- internal log
	}

	if logStore != nil {
		_ = logStore.Log(automationlog.Entry{
			Timestamp:  start.Unix(),
			RuleID:     trigger.ID,
			RuleName:   trigger.Name,
			RuleType:   "trigger",
			WebhookID:  webhook.ID,
			WebhookURL: expandedURL,
			Status:     finalStatus,
			HTTPStatus: lastHTTPStatus,
			DurationMs: time.Since(start).Milliseconds(),
			Error:      lastError,
			Attempt:    lastAttempt,
		})
	}
}
