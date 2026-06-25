// Package governance provides request routing rules, rate limiting, circuit
// breaking, concurrency limiting and canary release policies for gofly services.
package governance

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	coreruntime "github.com/imajinyun/gofly/core/runtime"
	controladmin "github.com/imajinyun/gofly/ops/admin"
)

// AdminOption customises the admin HTTP handler.
type AdminOption func(*Admin)

// Admin exposes governance state over HTTP for the control plane.
type Admin struct {
	rules        *RuleSet
	registry     *Registry
	manager      *Manager
	pathPrefix   string
	defaultReq   Request
	authorize    func(*http.Request) bool
	unauthorized func(http.ResponseWriter)
	runtime      *coreruntime.Registry
}

// AdminSnapshot is the JSON response for the governance admin endpoint.
type AdminSnapshot struct {
	Components   []ComponentSnapshot  `json:"components,omitempty"`
	Rules        []Rule               `json:"rules,omitempty"`
	RuleStats    []RuleStats          `json:"ruleStats,omitempty"`
	Diagnostics  []RuleDiagnostic     `json:"diagnostics,omitempty"`
	RuleStatus   RuleSetStatus        `json:"ruleStatus,omitempty"`
	RuleEvents   []RuleSetEvent       `json:"ruleEvents,omitempty"`
	RuleVersions []RuleSetVersion     `json:"ruleVersions,omitempty"`
	Manager      *ManagerSnapshot     `json:"manager,omitempty"`
	Runtime      coreruntime.Snapshot `json:"runtime,omitempty"`
}

type rollbackRequest struct {
	Version         int64  `json:"version"`
	ExpectedVersion *int64 `json:"expectedVersion,omitempty"`
	RequireSafe     bool   `json:"requireSafe,omitempty"`
	Force           bool   `json:"force,omitempty"`
}

type rulesUpdateRequest struct {
	Rules   []Rule        `json:"rules"`
	Persist bool          `json:"persist,omitempty"`
	TTL     time.Duration `json:"ttl,omitempty"`
}

type RulePlan struct {
	OK              bool             `json:"ok"`
	Safe            bool             `json:"safe"`
	Risk            string           `json:"risk"`
	Rules           int              `json:"rules"`
	Persist         bool             `json:"persist,omitempty"`
	TTL             time.Duration    `json:"ttl,omitempty"`
	ValidationError string           `json:"validationError,omitempty"`
	Diagnostics     []RuleDiagnostic `json:"diagnostics,omitempty"`
	Diff            RuleDiff         `json:"diff,omitempty"`
	Impact          RulePlanImpact   `json:"impact"`
}

type RulePlanImpact struct {
	Added       int `json:"added,omitempty"`
	Removed     int `json:"removed,omitempty"`
	Changed     int `json:"changed,omitempty"`
	Unchanged   int `json:"unchanged,omitempty"`
	Info        int `json:"info,omitempty"`
	Warnings    int `json:"warnings,omitempty"`
	Errors      int `json:"errors,omitempty"`
	ReviewItems int `json:"reviewItems,omitempty"`
}

type eventFilter struct {
	Action        string
	Source        string
	SuccessSet    bool
	Success       bool
	MinVersionSet bool
	MinVersion    int64
	LimitSet      bool
	Limit         int
}

func NewAdmin(rules *RuleSet, registry *Registry, opts ...AdminOption) *Admin {
	a := &Admin{rules: rules, registry: registry}
	for _, opt := range opts {
		if opt != nil {
			opt(a)
		}
	}
	return a
}

func WithAdminPathPrefix(prefix string) AdminOption {
	return func(a *Admin) {
		a.pathPrefix = cleanAdminPath(prefix)
	}
}

func WithAdminDefaultRequest(req Request) AdminOption {
	return func(a *Admin) {
		a.defaultReq = normalizeRequest(req)
	}
}

func WithAdminAuthorization(authorize func(*http.Request) bool, unauthorized func(http.ResponseWriter)) AdminOption {
	return func(a *Admin) {
		a.authorize = authorize
		a.unauthorized = unauthorized
	}
}

func WithAdminManager(manager *Manager) AdminOption {
	return func(a *Admin) {
		a.manager = manager
		if manager != nil {
			a.rules = manager.RuleSet()
		}
	}
}

func WithAdminRuntimeRegistry(registry *coreruntime.Registry) AdminOption {
	return func(a *Admin) {
		a.runtime = registry
	}
}

func (a *Admin) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if a == nil {
		writeAdminError(w, http.StatusServiceUnavailable, "governance admin is nil")
		return
	}
	if a.authorize != nil && !a.authorize(r) {
		if a.unauthorized != nil {
			a.unauthorized(w)
			return
		}
		writeAdminError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	path := a.relativePath(r.URL.Path)
	switch path {
	case "", "/", "/snapshot":
		a.handleSnapshot(w, r)
	case "/components":
		a.handleComponents(w, r)
	case "/rules":
		a.handleRules(w, r)
	case "/status":
		a.handleStatus(w, r)
	case "/stats":
		a.handleStats(w, r)
	case "/diagnostics":
		a.handleDiagnostics(w, r)
	case "/metrics":
		a.handleMetrics(w, r)
	case "/runtime":
		a.handleRuntime(w, r)
	case "/history":
		a.handleHistory(w, r)
	case "/events":
		a.handleEvents(w, r)
	case "/versions":
		a.handleVersions(w, r)
	case "/diff":
		a.handleDiff(w, r)
	case "/plan":
		a.handlePlan(w, r)
	case "/rollback":
		a.handleRollback(w, r)
	case "/reload":
		a.handleReload(w, r)
	case "/validate":
		a.handleValidate(w, r)
	case "/explain":
		a.handleExplain(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (a *Admin) Snapshot() AdminSnapshot {
	if a == nil {
		return AdminSnapshot{}
	}
	snapshot := AdminSnapshot{}
	if a.registry != nil {
		snapshot.Components = a.registry.Snapshots()
	}
	if a.manager != nil {
		managerSnapshot := a.manager.Snapshot()
		snapshot.Manager = &managerSnapshot
		if len(managerSnapshot.Runtime.Components) > 0 {
			snapshot.Runtime = managerSnapshot.Runtime
		}
	}
	if a.runtime != nil {
		snapshot.Runtime = a.runtime.Snapshot(context.Background())
	}
	if a.rules != nil {
		snapshot.Rules = a.rules.Snapshot()
		snapshot.RuleStats = a.rules.Stats()
		snapshot.Diagnostics = a.rules.Diagnostics()
		snapshot.RuleStatus = a.rules.Status()
		snapshot.RuleEvents = a.rules.History()
		snapshot.RuleVersions = a.rules.Versions()
	}
	return snapshot
}

func (a *Admin) Diagnostics() []RuleDiagnostic {
	if a == nil || a.rules == nil {
		return nil
	}
	return a.rules.Diagnostics()
}

func (a *Admin) PlanRules(rules []Rule, persist bool, ttl time.Duration) RulePlan {
	plan := RulePlan{Rules: len(rules), Persist: persist, TTL: ttl}
	diagnostics := AnalyzeRules(rules)
	plan.Diagnostics = diagnostics
	for _, diagnostic := range diagnostics {
		switch diagnostic.Severity {
		case DiagnosticError:
			plan.Impact.Errors++
		case DiagnosticWarn:
			plan.Impact.Warnings++
		case DiagnosticInfo:
			plan.Impact.Info++
		}
	}
	if err := ValidateRules(rules...); err != nil {
		plan.ValidationError = err.Error()
		if plan.Impact.Errors == 0 {
			plan.Impact.Errors++
		}
	} else {
		current := []Rule(nil)
		if a != nil && a.rules != nil {
			current = a.rules.Snapshot()
		}
		plan.Diff = DiffRules(current, rules)
		plan.Impact.Added = len(plan.Diff.Added)
		plan.Impact.Removed = len(plan.Diff.Removed)
		plan.Impact.Changed = len(plan.Diff.Changed)
		plan.Impact.Unchanged = plan.Diff.Unchanged
	}
	plan.Impact.ReviewItems = plan.Impact.Removed + plan.Impact.Changed + plan.Impact.Warnings + plan.Impact.Errors
	plan.OK = plan.ValidationError == "" && plan.Impact.Errors == 0
	plan.Safe = plan.OK && plan.Impact.Removed == 0 && plan.Impact.Warnings == 0
	plan.Risk = rulePlanRisk(plan)
	return plan
}

func (a *Admin) Explain(r *http.Request) RuleExplain {
	req := Request{}
	if a != nil {
		req = a.defaultReq
	}
	if r != nil {
		req = requestFromQuery(r, req)
	}
	if a == nil || a.rules == nil {
		return RuleExplain{Request: normalizeRequest(req)}
	}
	return a.rules.Explain(req)
}

func (a *Admin) ReplaceRules(rules []Rule) error {
	if a != nil && a.manager != nil {
		return a.manager.ReplaceRules(rules...)
	}
	if a == nil || a.rules == nil {
		return fmt.Errorf("governance rule set is nil")
	}
	return a.rules.ReplaceValidated(rules...)
}

func (a *Admin) SaveRules(ctx context.Context, rules []Rule, ttl time.Duration) error {
	if a == nil || a.manager == nil {
		return fmt.Errorf("governance manager is nil")
	}
	return a.manager.SaveRules(ctx, rules, ttl)
}

func (a *Admin) ReloadRules(ctx context.Context) error {
	if a == nil || a.manager == nil {
		return fmt.Errorf("governance manager is nil")
	}
	return a.manager.Reload(ctx)
}

func (a *Admin) RollbackRules(version int64) error {
	if a == nil || a.rules == nil {
		return fmt.Errorf("governance rule set is nil")
	}
	return a.rules.Rollback(version)
}

func (a *Admin) relativePath(path string) string {
	path = cleanAdminPath(path)
	if a == nil || a.pathPrefix == "" {
		return path
	}
	if path == a.pathPrefix {
		return "/"
	}
	if strings.HasPrefix(path, a.pathPrefix+"/") {
		return strings.TrimPrefix(path, a.pathPrefix)
	}
	return path
}

func (a *Admin) handleSnapshot(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAdminError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	writeAdminJSON(w, http.StatusOK, a.Snapshot())
}

func (a *Admin) handleComponents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAdminError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if a.registry == nil {
		writeAdminJSON(w, http.StatusOK, []ComponentSnapshot(nil))
		return
	}
	writeAdminJSON(w, http.StatusOK, a.registry.Snapshots())
}

func (a *Admin) handleRuntime(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAdminError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if a.runtime != nil {
		writeAdminJSON(w, http.StatusOK, a.runtime.Snapshot(r.Context()))
		return
	}
	if a.manager != nil {
		writeAdminJSON(w, http.StatusOK, a.manager.RuntimeSnapshot(r.Context()))
		return
	}
	writeAdminJSON(w, http.StatusOK, coreruntime.Snapshot{})
}

func (a *Admin) handleRules(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		if a.rules == nil {
			writeAdminJSON(w, http.StatusOK, []Rule(nil))
			return
		}
		writeAdminJSON(w, http.StatusOK, a.rules.Snapshot())
	case http.MethodPost, http.MethodPut:
		rules, persist, ttl, err := decodeRulesUpdate(r)
		if err != nil {
			writeAdminError(w, http.StatusBadRequest, "decode governance rules: "+err.Error())
			return
		}
		if status, err := a.checkRuleUpdateGuards(r, rules, persist, ttl); err != nil {
			writeAdminError(w, status, err.Error())
			return
		}
		if persist {
			err = a.SaveRules(r.Context(), rules, ttl)
		} else {
			err = a.ReplaceRules(rules)
		}
		if err != nil {
			writeAdminError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeAdminJSON(w, http.StatusOK, a.Snapshot())
	default:
		writeAdminError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (a *Admin) checkRuleUpdateGuards(r *http.Request, rules []Rule, persist bool, ttl time.Duration) (int, error) {
	if r == nil {
		return http.StatusOK, nil
	}
	values := r.URL.Query()
	if raw := strings.TrimSpace(values.Get("expectedVersion")); raw != "" {
		expected, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || expected < 0 {
			return http.StatusBadRequest, fmt.Errorf("invalid expectedVersion %q", raw)
		}
		current := int64(0)
		if a != nil && a.rules != nil {
			current = a.rules.Status().Version
		}
		if current != expected {
			return http.StatusConflict, fmt.Errorf("governance rule version mismatch: current=%d expected=%d", current, expected)
		}
	}
	requireSafe, err := parseOptionalAdminBool(values.Get("requireSafe"), "requireSafe")
	if err != nil {
		return http.StatusBadRequest, err
	}
	force, err := parseOptionalAdminBool(values.Get("force"), "force")
	if err != nil {
		return http.StatusBadRequest, err
	}
	if requireSafe && !force {
		plan := a.PlanRules(rules, persist, ttl)
		if !plan.Safe {
			return http.StatusConflict, fmt.Errorf("governance rule plan is not safe: risk=%s reviewItems=%d", plan.Risk, plan.Impact.ReviewItems)
		}
	}
	return http.StatusOK, nil
}

func (a *Admin) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAdminError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if a.rules == nil {
		writeAdminJSON(w, http.StatusOK, RuleSetStatus{})
		return
	}
	writeAdminJSON(w, http.StatusOK, a.rules.Status())
}

func (a *Admin) handleStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAdminError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if a.rules == nil {
		writeAdminJSON(w, http.StatusOK, []RuleStats(nil))
		return
	}
	writeAdminJSON(w, http.StatusOK, a.rules.Stats())
}

func (a *Admin) handleDiagnostics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAdminError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	writeAdminJSON(w, http.StatusOK, a.Diagnostics())
}

func (a *Admin) handleMetrics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAdminError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	data, err := renderAdminMetrics(a.rules)
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func renderAdminMetrics(rules *RuleSet) ([]byte, error) {
	if rules == nil {
		return nil, nil
	}
	var buf bytes.Buffer
	if err := rules.WritePrometheus(&buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func (a *Admin) handleHistory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAdminError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if a.rules == nil {
		writeAdminJSON(w, http.StatusOK, []RuleSetEvent(nil))
		return
	}
	writeAdminJSON(w, http.StatusOK, a.rules.History())
}

func (a *Admin) handleEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAdminError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if a.rules == nil {
		writeAdminJSON(w, http.StatusOK, []RuleSetEvent(nil))
		return
	}
	filter, err := parseAdminEventFilter(r)
	if err != nil {
		writeAdminError(w, http.StatusBadRequest, err.Error())
		return
	}
	values := r.URL.Query()
	wait, err := parseOptionalAdminBool(values.Get("wait"), "wait")
	if err != nil {
		writeAdminError(w, http.StatusBadRequest, err.Error())
		return
	}
	watch, err := parseOptionalAdminBool(values.Get("watch"), "watch")
	if err != nil {
		writeAdminError(w, http.StatusBadRequest, err.Error())
		return
	}
	stream, err := parseOptionalAdminBool(values.Get("stream"), "stream")
	if err != nil {
		writeAdminError(w, http.StatusBadRequest, err.Error())
		return
	}
	if wait {
		a.waitEvent(w, r, filter)
		return
	}
	if !watch && !stream {
		writeAdminJSON(w, http.StatusOK, filter.filterEvents(a.rules.History()))
		return
	}
	history, err := parseOptionalAdminBool(values.Get("history"), "history")
	if err != nil {
		writeAdminError(w, http.StatusBadRequest, err.Error())
		return
	}
	a.streamEvents(w, r, filter, history)
}

func (a *Admin) waitEvent(w http.ResponseWriter, r *http.Request, filter eventFilter) {
	timeout, err := parseAdminTimeout(r.URL.Query().Get("timeout"), 30*time.Second)
	if err != nil {
		writeAdminError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !filter.MinVersionSet {
		filter.MinVersion = a.rules.Status().Version
	}
	ctx := r.Context()
	var cancel context.CancelFunc
	if timeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	events, unsubscribe := a.rules.Subscribe(16)
	defer unsubscribe()
	for _, event := range a.rules.History() {
		if filter.match(event) {
			writeAdminJSON(w, http.StatusOK, event)
			return
		}
	}
	for {
		select {
		case <-ctx.Done():
			w.WriteHeader(http.StatusNoContent)
			return
		case event, ok := <-events:
			if !ok {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			if !filter.match(event) {
				continue
			}
			writeAdminJSON(w, http.StatusOK, event)
			return
		}
	}
}

func (a *Admin) streamEvents(w http.ResponseWriter, r *http.Request, filter eventFilter, includeHistory bool) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeAdminError(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}
	w.Header().Set("Content-Type", "application/x-ndjson; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	events, unsubscribe := a.rules.Subscribe(16)
	defer unsubscribe()
	encoder := json.NewEncoder(w)
	if includeHistory {
		for _, event := range filter.filterEvents(a.rules.History()) {
			if err := encoder.Encode(event); err != nil {
				return
			}
		}
		flusher.Flush()
	}
	for {
		select {
		case <-r.Context().Done():
			return
		case event, ok := <-events:
			if !ok {
				return
			}
			if !filter.match(event) {
				continue
			}
			if err := encoder.Encode(event); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func (a *Admin) handleVersions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAdminError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if a.rules == nil {
		writeAdminJSON(w, http.StatusOK, []RuleSetVersion(nil))
		return
	}
	writeAdminJSON(w, http.StatusOK, a.rules.Versions())
}

func (a *Admin) handleDiff(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		a.handleVersionDiff(w, r)
	case http.MethodPost:
		rules, _, _, err := decodeRulesUpdate(r)
		if err != nil {
			writeAdminError(w, http.StatusBadRequest, "decode governance rules: "+err.Error())
			return
		}
		if err := ValidateRules(rules...); err != nil {
			writeAdminError(w, http.StatusBadRequest, err.Error())
			return
		}
		current := []Rule(nil)
		if a.rules != nil {
			current = a.rules.Snapshot()
		}
		writeAdminJSON(w, http.StatusOK, DiffRules(current, rules))
	default:
		writeAdminError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (a *Admin) handlePlan(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAdminError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	rules, persist, ttl, err := decodeRulesUpdate(r)
	if err != nil {
		writeAdminError(w, http.StatusBadRequest, "decode governance rules: "+err.Error())
		return
	}
	writeAdminJSON(w, http.StatusOK, a.PlanRules(rules, persist, ttl))
}

func (a *Admin) handleVersionDiff(w http.ResponseWriter, r *http.Request) {
	if a.rules == nil {
		writeAdminJSON(w, http.StatusOK, RuleDiff{})
		return
	}
	version, err := adminQueryVersion(r)
	if err != nil {
		writeAdminError(w, http.StatusBadRequest, err.Error())
		return
	}
	if version == 0 {
		writeAdminJSON(w, http.StatusOK, DiffRules(nil, a.rules.Snapshot()))
		return
	}
	for _, item := range a.rules.Versions() {
		if item.Version == version {
			writeAdminJSON(w, http.StatusOK, DiffRules(item.Rules, a.rules.Snapshot()))
			return
		}
	}
	writeAdminError(w, http.StatusNotFound, fmt.Sprintf("governance rule version %d not found", version))
}

func (a *Admin) handleRollback(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAdminError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	rollback, err := parseRollbackRequest(r)
	if err != nil {
		writeAdminError(w, http.StatusBadRequest, err.Error())
		return
	}
	if status, err := a.checkRollbackGuards(rollback); err != nil {
		writeAdminError(w, status, err.Error())
		return
	}
	if err := a.RollbackRules(rollback.Version); err != nil {
		writeAdminError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeAdminJSON(w, http.StatusOK, a.Snapshot())
}

func (a *Admin) checkRollbackGuards(rollback rollbackRequest) (int, error) {
	if rollback.ExpectedVersion != nil {
		current := int64(0)
		if a != nil && a.rules != nil {
			current = a.rules.Status().Version
		}
		if current != *rollback.ExpectedVersion {
			return http.StatusConflict, fmt.Errorf("governance rule version mismatch: current=%d expected=%d", current, *rollback.ExpectedVersion)
		}
	}
	if rollback.RequireSafe && !rollback.Force {
		target, ok := a.rulesForVersion(rollback.Version)
		if !ok {
			return http.StatusBadRequest, fmt.Errorf("governance rule version %d not found", rollback.Version)
		}
		plan := a.PlanRules(target, false, 0)
		if !plan.Safe {
			return http.StatusConflict, fmt.Errorf("governance rollback plan is not safe: risk=%s reviewItems=%d", plan.Risk, plan.Impact.ReviewItems)
		}
	}
	return http.StatusOK, nil
}

func (a *Admin) rulesForVersion(version int64) ([]Rule, bool) {
	if a == nil || a.rules == nil {
		return nil, false
	}
	for _, item := range a.rules.Versions() {
		if item.Version == version {
			return cloneRules(item.Rules), true
		}
	}
	return nil, false
}

func (a *Admin) handleReload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAdminError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if err := a.ReloadRules(r.Context()); err != nil {
		writeAdminError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeAdminJSON(w, http.StatusOK, a.Snapshot())
}

func (a *Admin) handleValidate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAdminError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	rules, _, _, err := decodeRulesUpdate(r)
	if err != nil {
		writeAdminError(w, http.StatusBadRequest, "decode governance rules: "+err.Error())
		return
	}
	if err := ValidateRules(rules...); err != nil {
		writeAdminError(w, http.StatusBadRequest, err.Error())
		return
	}
	plan := a.PlanRules(rules, false, 0)
	writeAdminJSON(w, http.StatusOK, map[string]any{"ok": true, "safe": plan.Safe, "rules": len(rules), "diagnostics": plan.Diagnostics, "impact": plan.Impact, "risk": plan.Risk})
}

func rulePlanRisk(plan RulePlan) string {
	if !plan.OK || plan.Impact.Errors > 0 {
		return "high"
	}
	if plan.Impact.Removed > 0 || plan.Impact.Warnings > 0 {
		return "medium"
	}
	if plan.Impact.Changed > 0 || plan.Impact.Added > 0 {
		return "low"
	}
	return "none"
}

func (a *Admin) handleExplain(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAdminError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	writeAdminJSON(w, http.StatusOK, a.Explain(r))
}

func rollbackVersion(r *http.Request) (int64, error) {
	rollback, err := parseRollbackRequest(r)
	if err != nil {
		return 0, err
	}
	return rollback.Version, nil
}

func parseRollbackRequest(r *http.Request) (rollbackRequest, error) {
	rollback := rollbackRequest{}
	if r == nil {
		return rollback, fmt.Errorf("rollback request is nil")
	}
	values := r.URL.Query()
	if raw := values.Get("expectedVersion"); raw != "" {
		expected, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || expected < 0 {
			return rollback, fmt.Errorf("invalid expectedVersion %q", raw)
		}
		rollback.ExpectedVersion = &expected
	}
	requireSafe, err := parseOptionalAdminBool(values.Get("requireSafe"), "requireSafe")
	if err != nil {
		return rollback, err
	}
	force, err := parseOptionalAdminBool(values.Get("force"), "force")
	if err != nil {
		return rollback, err
	}
	rollback.RequireSafe = requireSafe
	rollback.Force = force
	if raw := r.URL.Query().Get("version"); raw != "" {
		version, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || version <= 0 {
			return rollback, fmt.Errorf("invalid rollback version %q", raw)
		}
		rollback.Version = version
		return rollback, nil
	}
	if err := json.NewDecoder(r.Body).Decode(&rollback); err != nil {
		return rollback, fmt.Errorf("decode rollback request: %w", err)
	}
	if rollback.Version <= 0 {
		return rollback, fmt.Errorf("rollback version must be positive")
	}
	if rollback.ExpectedVersion != nil && *rollback.ExpectedVersion < 0 {
		return rollback, fmt.Errorf("invalid expectedVersion %d", *rollback.ExpectedVersion)
	}
	if requireSafe {
		rollback.RequireSafe = true
	}
	if force {
		rollback.Force = true
	}
	return rollback, nil
}

func adminQueryVersion(r *http.Request) (int64, error) {
	if r == nil {
		return 0, fmt.Errorf("request is nil")
	}
	raw := r.URL.Query().Get("version")
	if raw == "" {
		return 0, nil
	}
	version, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || version <= 0 {
		return 0, fmt.Errorf("invalid governance rule version %q", raw)
	}
	return version, nil
}

func decodeRulesUpdate(r *http.Request) ([]Rule, bool, time.Duration, error) {
	if r == nil {
		return nil, false, 0, fmt.Errorf("rules request is nil")
	}
	var raw json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
		return nil, false, 0, err
	}
	persist, err := parseOptionalAdminBool(r.URL.Query().Get("persist"), "persist")
	if err != nil {
		return nil, false, 0, err
	}
	ttl, err := parseAdminDuration(r.URL.Query().Get("ttl"))
	if err != nil {
		return nil, false, 0, err
	}
	trimmed := strings.TrimSpace(string(raw))
	if strings.HasPrefix(trimmed, "[") {
		var rules []Rule
		if err := json.Unmarshal(raw, &rules); err != nil {
			return nil, false, 0, err
		}
		return rules, persist, ttl, nil
	}
	var req rulesUpdateRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, false, 0, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, false, 0, err
	}
	if _, ok := fields["rules"]; !ok {
		return nil, false, 0, fmt.Errorf("rules field is required")
	}
	if strings.TrimSpace(string(fields["rules"])) == "null" {
		return nil, false, 0, fmt.Errorf("rules field must be an array")
	}
	if req.Persist {
		persist = true
	}
	if req.TTL < 0 {
		return nil, false, 0, fmt.Errorf("invalid ttl %q", req.TTL.String())
	}
	if req.TTL > 0 {
		ttl = req.TTL
	}
	return req.Rules, persist, ttl, nil
}

func parseOptionalAdminBool(raw, name string) (bool, error) {
	if strings.TrimSpace(raw) == "" {
		return false, nil
	}
	return parseAdminBoolParam(raw, name)
}

func parseAdminDuration(raw string) (time.Duration, error) {
	return parseAdminDurationParam(raw, "ttl")
}

func parseAdminDurationParam(raw string, name string) (time.Duration, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, nil
	}
	if d, err := time.ParseDuration(raw); err == nil {
		if d < 0 {
			return 0, fmt.Errorf("invalid %s %q", name, raw)
		}
		return d, nil
	}
	seconds, err := strconv.ParseFloat(raw, 64)
	if err != nil || math.IsNaN(seconds) || math.IsInf(seconds, 0) || seconds < 0 {
		return 0, fmt.Errorf("invalid %s %q", name, raw)
	}
	const maxDurationSeconds = float64(1<<63-1) / float64(time.Second)
	if seconds > maxDurationSeconds {
		return 0, fmt.Errorf("invalid %s %q", name, raw)
	}
	return time.Duration(seconds * float64(time.Second)), nil
}

func parseAdminTimeout(raw string, fallback time.Duration) (time.Duration, error) {
	if strings.TrimSpace(raw) == "" {
		return fallback, nil
	}
	timeout, err := parseAdminDurationParam(raw, "timeout")
	if err != nil {
		return 0, err
	}
	return timeout, nil
}

func parseAdminEventFilter(r *http.Request) (eventFilter, error) {
	filter := eventFilter{}
	if r == nil {
		return filter, nil
	}
	values := r.URL.Query()
	filter.Action = strings.ToLower(strings.TrimSpace(values.Get("action")))
	filter.Source = strings.TrimSpace(values.Get("source"))
	successSet := false
	successValue := false
	if raw := values.Get("success"); raw != "" {
		success, err := parseAdminBoolParam(raw, "success")
		if err != nil {
			return filter, err
		}
		successSet = true
		successValue = success
		filter.SuccessSet = true
		filter.Success = success
	}
	failedSet := false
	if raw := values.Get("failed"); raw != "" {
		failed, err := parseAdminBoolParam(raw, "failed")
		if err != nil {
			return filter, err
		}
		if failed {
			failedSet = true
			filter.SuccessSet = true
			filter.Success = false
		}
	}
	if raw := values.Get("failure"); raw != "" {
		failed, err := parseAdminBoolParam(raw, "failure")
		if err != nil {
			return filter, err
		}
		if failed {
			failedSet = true
			filter.SuccessSet = true
			filter.Success = false
		}
	}
	if successSet && successValue && failedSet {
		return filter, fmt.Errorf("success filter conflicts with failed filter")
	}
	if filter.SuccessSet && !filter.Success {
		filter.SuccessSet = true
		filter.Success = false
	}
	if raw := strings.TrimSpace(values.Get("since")); raw != "" {
		version, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || version < 0 {
			return filter, fmt.Errorf("invalid since %q", raw)
		}
		filter.MinVersionSet = true
		filter.MinVersion = version
	}
	if raw := strings.TrimSpace(values.Get("minVersion")); raw != "" {
		version, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || version < 0 {
			return filter, fmt.Errorf("invalid minVersion %q", raw)
		}
		filter.MinVersionSet = true
		filter.MinVersion = version
	}
	if raw := strings.TrimSpace(values.Get("limit")); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil || limit < 0 {
			return filter, fmt.Errorf("invalid limit %q", raw)
		}
		filter.LimitSet = true
		filter.Limit = limit
	}
	return filter, nil
}

func parseAdminBoolParam(raw, name string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "y", "on":
		return true, nil
	case "0", "false", "no", "n", "off":
		return false, nil
	default:
		return false, fmt.Errorf("invalid %s %q", name, raw)
	}
}

func (f eventFilter) filterEvents(events []RuleSetEvent) []RuleSetEvent {
	if len(events) == 0 || f.LimitSet && f.Limit == 0 {
		return nil
	}
	out := make([]RuleSetEvent, 0, len(events))
	for _, event := range events {
		if f.match(event) {
			out = append(out, event)
		}
	}
	if f.Limit > 0 && len(out) > f.Limit {
		out = out[len(out)-f.Limit:]
	}
	return out
}

func (f eventFilter) match(event RuleSetEvent) bool {
	if f.Action != "" && strings.ToLower(event.Action) != f.Action {
		return false
	}
	if f.Source != "" && event.Source != f.Source {
		return false
	}
	if f.SuccessSet && event.Success != f.Success {
		return false
	}
	if f.MinVersion > 0 && event.Version <= f.MinVersion {
		return false
	}
	return true
}

func requestFromQuery(r *http.Request, base Request) Request {
	if r == nil {
		return normalizeRequest(base)
	}
	values := r.URL.Query()
	if value := values.Get("transport"); value != "" {
		base.Transport = value
	}
	if value := values.Get("service"); value != "" {
		base.Service = value
	}
	if value := values.Get("method"); value != "" {
		base.Method = value
	}
	if value := values.Get("path"); value != "" {
		base.Path = value
	}
	if tags := parseAdminTags(values.Get("tags")); len(tags) > 0 {
		base.Tags = tags
	}
	return normalizeRequest(base)
}

func parseAdminTags(raw string) map[string]string {
	if raw == "" {
		return nil
	}
	out := make(map[string]string)
	for _, item := range strings.Split(raw, ",") {
		key, value, ok := strings.Cut(item, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		out[key] = strings.TrimSpace(value)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func cleanAdminPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	path = strings.TrimRight(path, "/")
	if path == "" {
		return "/"
	}
	return path
}

func writeAdminJSON(w http.ResponseWriter, status int, value any) {
	controladmin.WriteJSON(w, status, value)
}

func writeAdminError(w http.ResponseWriter, status int, message string) {
	controladmin.WriteError(w, status, message)
}
