package service

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// PromptInjectionRule is a database-backed rule for adding an operator prompt
// to supported gateway request formats. Prompt tokens intentionally remain part
// of the normal upstream input usage and therefore follow the existing billing
// path without any special accounting.
type PromptInjectionRule struct {
	ID             int64     `json:"id"`
	Name           string    `json:"name"`
	Description    string    `json:"description,omitempty"`
	PromptContent  string    `json:"prompt_content"`
	Enabled        bool      `json:"enabled"`
	ModelMatchType string    `json:"model_match_type"` // exact or prefix
	ModelPattern   string    `json:"model_pattern"`
	GroupMode      string    `json:"group_mode"` // all, include, exclude
	GroupIDs       []int64   `json:"group_ids,omitempty"`
	Endpoint       string    `json:"endpoint"`  // any, chat_completions, responses, messages, gemini
	Placement      string    `json:"placement"` // system_prepend or system_append
	Priority       int       `json:"priority"`
	FailurePolicy  string    `json:"failure_policy"` // skip or reject
	Version        int64     `json:"version"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type promptInjectionCache struct {
	rules  []PromptInjectionRule
	loaded time.Time
}

// PromptInjectionService evaluates rules and mutates supported JSON request
// bodies. It is deliberately independent of account selection so failover can
// reuse the same already-built body without duplicate injection.
type PromptInjectionService struct {
	settings *SettingService
	mu       sync.Mutex
	cache    promptInjectionCache
}

func NewPromptInjectionService(settings *SettingService) *PromptInjectionService {
	return &PromptInjectionService{settings: settings}
}

func normalizePromptRule(r PromptInjectionRule) PromptInjectionRule {
	if r.ModelMatchType != "prefix" {
		r.ModelMatchType = "exact"
	}
	if r.GroupMode != "include" && r.GroupMode != "exclude" {
		r.GroupMode = "all"
	}
	if r.Endpoint != "chat_completions" && r.Endpoint != "responses" && r.Endpoint != "messages" && r.Endpoint != "gemini" {
		r.Endpoint = "any"
	}
	if r.Placement != "system_append" {
		r.Placement = "system_prepend"
	}
	if r.FailurePolicy != "reject" {
		r.FailurePolicy = "skip"
	}
	r.ModelPattern = strings.TrimSpace(r.ModelPattern)
	return r
}

func (s *SettingService) GetPromptInjectionRules(ctx context.Context) ([]PromptInjectionRule, error) {
	if s == nil || s.settingRepo == nil {
		return nil, nil
	}
	raw, err := s.settingRepo.GetValue(ctx, SettingKeyPromptInjectionRules)
	if err != nil {
		if err == ErrSettingNotFound {
			return nil, nil
		}
		return nil, err
	}
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	var rules []PromptInjectionRule
	if err := json.Unmarshal([]byte(raw), &rules); err != nil {
		return nil, fmt.Errorf("decode prompt injection rules: %w", err)
	}
	for i := range rules {
		rules[i] = normalizePromptRule(rules[i])
	}
	return rules, nil
}

func (s *SettingService) SavePromptInjectionRules(ctx context.Context, rules []PromptInjectionRule) error {
	if s == nil || s.settingRepo == nil {
		return fmt.Errorf("settings repository is not configured")
	}
	now := time.Now().UTC()
	seen := map[int64]bool{}
	for i := range rules {
		rules[i] = normalizePromptRule(rules[i])
		if rules[i].ID <= 0 {
			rules[i].ID = now.UnixNano() + int64(i)
		}
		if seen[rules[i].ID] {
			return fmt.Errorf("duplicate prompt injection rule id: %d", rules[i].ID)
		}
		seen[rules[i].ID] = true
		if rules[i].Name == "" {
			return fmt.Errorf("prompt injection rule name is required")
		}
		if rules[i].PromptContent == "" {
			return fmt.Errorf("prompt injection content is required")
		}
		if rules[i].ModelPattern == "" {
			return fmt.Errorf("prompt injection model pattern is required")
		}
		rules[i].Version++
		rules[i].UpdatedAt = now
	}
	b, err := json.Marshal(rules)
	if err != nil {
		return err
	}
	if err := s.settingRepo.Set(ctx, SettingKeyPromptInjectionRules, string(b)); err != nil {
		return err
	}
	if s.onUpdate != nil {
		s.onUpdate()
	}
	return nil
}

func (s *PromptInjectionService) rules(ctx context.Context) ([]PromptInjectionRule, error) {
	if s == nil || s.settings == nil {
		return nil, nil
	}
	s.mu.Lock()
	if time.Since(s.cache.loaded) < 5*time.Second {
		r := append([]PromptInjectionRule(nil), s.cache.rules...)
		s.mu.Unlock()
		return r, nil
	}
	s.mu.Unlock()
	rules, err := s.settings.GetPromptInjectionRules(ctx)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.cache = promptInjectionCache{rules: append([]PromptInjectionRule(nil), rules...), loaded: time.Now()}
	s.mu.Unlock()
	return rules, nil
}

func endpointMatches(rule, endpoint string) bool { return rule == "any" || rule == endpoint }

func groupMatches(r PromptInjectionRule, groupID *int64) bool {
	if r.GroupMode == "all" {
		return true
	}
	id := int64(0)
	if groupID != nil {
		id = *groupID
	}
	found := false
	for _, v := range r.GroupIDs {
		if v == id {
			found = true
			break
		}
	}
	if r.GroupMode == "exclude" {
		return !found
	}
	return found
}

func modelMatches(r PromptInjectionRule, model string) bool {
	if r.ModelMatchType == "prefix" {
		return strings.HasPrefix(model, r.ModelPattern)
	}
	return model == r.ModelPattern
}

func matchingPromptRules(rules []PromptInjectionRule, model string, groupID *int64, endpoint string) []PromptInjectionRule {
	matched := make([]PromptInjectionRule, 0)
	for _, r := range rules {
		if !r.Enabled || r.PromptContent == "" || !modelMatches(r, model) || !groupMatches(r, groupID) || !endpointMatches(r.Endpoint, endpoint) {
			continue
		}
		matched = append(matched, r)
	}
	sort.SliceStable(matched, func(i, j int) bool {
		a, b := matched[i], matched[j]
		if a.ModelMatchType != b.ModelMatchType {
			return a.ModelMatchType == "exact"
		}
		if (a.Endpoint == "any") != (b.Endpoint == "any") {
			return a.Endpoint != "any"
		}
		if len(a.ModelPattern) != len(b.ModelPattern) {
			return len(a.ModelPattern) > len(b.ModelPattern)
		}
		if a.Priority != b.Priority {
			return a.Priority < b.Priority
		}
		return a.ID < b.ID
	})
	return matched
}

func combinePrompt(current, prompt string, prepend bool) string {
	if strings.TrimSpace(current) == "" {
		return prompt
	}
	if prepend {
		return prompt + "\n\n" + current
	}
	return current + "\n\n" + prompt
}

func injectChat(body []byte, prompt string, prepend bool) ([]byte, error) {
	msgs := gjson.GetBytes(body, "messages")
	if !msgs.Exists() || !msgs.IsArray() {
		return nil, fmt.Errorf("messages is missing or not an array")
	}
	msg := map[string]any{"role": "system", "content": prompt}
	return insertArrayElement(body, "messages", msg, prepend)
}

func insertArrayElement(body []byte, path string, value any, prepend bool) ([]byte, error) {
	var items []json.RawMessage
	if err := json.Unmarshal([]byte(gjson.GetBytes(body, path).Raw), &items); err != nil {
		return nil, err
	}
	b, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	raw := json.RawMessage(b)
	if prepend {
		items = append([]json.RawMessage{raw}, items...)
	} else {
		items = append(items, raw)
	}
	return sjson.SetBytes(body, path, items)
}

func injectAnthropic(body []byte, prompt string, prepend bool) ([]byte, error) {
	sys := gjson.GetBytes(body, "system")
	if !sys.Exists() || sys.Type == gjson.String {
		current := sys.String()
		return sjson.SetBytes(body, "system", combinePrompt(current, prompt, prepend))
	}
	if !sys.IsArray() {
		return nil, fmt.Errorf("system is not a string or array")
	}
	block := map[string]any{"type": "text", "text": prompt}
	return insertArrayElement(body, "system", block, prepend)
}

func injectResponses(body []byte, prompt string, prepend bool) ([]byte, error) {
	current := gjson.GetBytes(body, "instructions").String()
	return sjson.SetBytes(body, "instructions", combinePrompt(current, prompt, prepend))
}

func injectGemini(body []byte, prompt string, prepend bool) ([]byte, error) {
	path := "systemInstruction.parts"
	parts := gjson.GetBytes(body, path)
	part := map[string]any{"text": prompt}
	if !parts.Exists() {
		return sjson.SetBytes(body, path, []any{part})
	}
	if !parts.IsArray() {
		return nil, fmt.Errorf("systemInstruction.parts is not an array")
	}
	return insertArrayElement(body, path, part, prepend)
}

// Apply injects all matching rules once. The caller should replace its body
// with the returned value before billing reservation and forwarding.
func (s *PromptInjectionService) Apply(ctx context.Context, model string, groupID *int64, endpoint string, body []byte) ([]byte, bool, error) {
	rules, err := s.rules(ctx)
	if err != nil {
		return body, false, err
	}
	matched := matchingPromptRules(rules, model, groupID, endpoint)
	if len(matched) == 0 {
		return body, false, nil
	}
	out := append([]byte(nil), body...)
	changed := false
	for _, r := range matched {
		prepend := r.Placement != "system_append"
		var next []byte
		switch endpoint {
		case "chat_completions":
			next, err = injectChat(out, r.PromptContent, prepend)
		case "responses":
			next, err = injectResponses(out, r.PromptContent, prepend)
		case "messages":
			next, err = injectAnthropic(out, r.PromptContent, prepend)
		case "gemini":
			next, err = injectGemini(out, r.PromptContent, prepend)
		default:
			continue
		}
		if err != nil {
			if r.FailurePolicy == "reject" {
				return body, false, fmt.Errorf("prompt injection rule %q: %w", r.Name, err)
			}
			continue
		}
		out, changed = next, true
	}
	return out, changed, nil
}
