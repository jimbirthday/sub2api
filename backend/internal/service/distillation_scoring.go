package service

import (
	"bytes"
	"encoding/json"
	"io"
)

// These configurable ratios affect scoring only. Safety sample floors, cohort
// baseline requirements and disposition durations remain server contracts.
type DistillationScoring struct {
	ConcurrencySaturation float64 `json:"concurrency_saturation"`
	RequestRateSaturation float64 `json:"request_rate_saturation"`
	PromptRepeatRatio     float64 `json:"prompt_repeat_ratio"`
	InterarrivalCV        float64 `json:"interarrival_cv"`
	OutputInputRatio      float64 `json:"output_input_ratio"`
}

func DefaultDistillationScoring() DistillationScoring {
	return DistillationScoring{0.5, 0.2, 0.9, 0.15, 4}
}
func ParseDistillationScoring(raw json.RawMessage) (DistillationScoring, error) {
	v := DefaultDistillationScoring()
	if len(raw) == 0 {
		return v, nil
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return v, ErrDistillationInvalid
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&v) != nil || d.Decode(new(any)) != io.EOF {
		return v, ErrDistillationInvalid
	}
	if !finiteDistillationValue(v.ConcurrencySaturation) || v.ConcurrencySaturation <= 0 || v.ConcurrencySaturation > 1 || !finiteDistillationValue(v.RequestRateSaturation) || v.RequestRateSaturation <= 0 || v.RequestRateSaturation > 1 || !finiteDistillationValue(v.PromptRepeatRatio) || v.PromptRepeatRatio <= 0 || v.PromptRepeatRatio > 1 || !finiteDistillationValue(v.InterarrivalCV) || v.InterarrivalCV <= 0 || v.InterarrivalCV > 1 || !finiteDistillationValue(v.OutputInputRatio) || v.OutputInputRatio < 1 || v.OutputInputRatio > 100 {
		return v, ErrDistillationInvalid
	}
	return v, nil
}
