package service

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type promptSettingRepo struct{ value string }

func (r *promptSettingRepo) Get(context.Context, string) (*Setting, error)    { return nil, nil }
func (r *promptSettingRepo) GetValue(context.Context, string) (string, error) { return r.value, nil }
func (r *promptSettingRepo) Set(_ context.Context, _ string, value string) error {
	r.value = value
	return nil
}
func (r *promptSettingRepo) GetMultiple(context.Context, []string) (map[string]string, error) {
	return nil, nil
}
func (r *promptSettingRepo) SetMultiple(context.Context, map[string]string) error { return nil }
func (r *promptSettingRepo) GetAll(context.Context) (map[string]string, error)    { return nil, nil }
func (r *promptSettingRepo) Delete(context.Context, string) error                 { return nil }

func TestPromptInjectionApplyFormatsAndGroup(t *testing.T) {
	repo := &promptSettingRepo{value: `[{"id":1,"name":"chat","prompt_content":"be concise","enabled":true,"model_match_type":"prefix","model_pattern":"gpt-","group_mode":"include","group_ids":[7],"endpoint":"chat_completions","placement":"system_prepend"},{"id":2,"name":"resp","prompt_content":"safe","enabled":true,"model_match_type":"exact","model_pattern":"gpt-4","group_mode":"all","endpoint":"responses","placement":"system_append"}]`}
	settings := NewSettingService(repo, &config.Config{})
	svc := NewPromptInjectionService(settings)
	gid := int64(7)
	out, changed, err := svc.Apply(context.Background(), "gpt-4", &gid, "chat_completions", []byte(`{"model":"gpt-4","messages":[{"role":"user","content":"hi"}]}`))
	require.NoError(t, err)
	require.True(t, changed)
	require.Contains(t, string(out), "be concise")
	out, changed, err = svc.Apply(context.Background(), "gpt-4", &gid, "responses", []byte(`{"model":"gpt-4","input":"hi"}`))
	require.NoError(t, err)
	require.True(t, changed)
	require.Contains(t, string(out), `"instructions":"safe"`)
	other := int64(8)
	_, changed, err = svc.Apply(context.Background(), "gpt-4", &other, "chat_completions", []byte(`{"messages":[]}`))
	require.NoError(t, err)
	require.False(t, changed)
}

func TestPromptInjectionAnthropicAndGemini(t *testing.T) {
	repo := &promptSettingRepo{value: `[{"id":1,"name":"all","prompt_content":"policy","enabled":true,"model_match_type":"exact","model_pattern":"m","group_mode":"all","endpoint":"messages","placement":"system_append"},{"id":2,"name":"gem","prompt_content":"gem policy","enabled":true,"model_match_type":"exact","model_pattern":"m","group_mode":"all","endpoint":"gemini","placement":"system_prepend"}]`}
	svc := NewPromptInjectionService(NewSettingService(repo, &config.Config{}))
	out, changed, err := svc.Apply(context.Background(), "m", nil, "messages", []byte(`{"system":"base","messages":[]}`))
	require.NoError(t, err)
	require.True(t, changed)
	require.Contains(t, string(out), "base\\n\\npolicy")
	out, changed, err = svc.Apply(context.Background(), "m", nil, "gemini", []byte(`{"contents":[]}`))
	require.NoError(t, err)
	require.True(t, changed)
	require.Contains(t, string(out), "gem policy")
}
