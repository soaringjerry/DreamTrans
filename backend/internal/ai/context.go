// Package ai contains provider-independent AI context assembly.
package ai

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
)

const (
	DefaultContextTokens = 64_000
	DefaultMaxTokens     = 256_000
	DefaultOutputReserve = 4_096
)

var ErrContextTooLarge = errors.New("requested full context exceeds the configured token limit")

type ContextPolicy struct {
	Mode      string `json:"mode,omitempty"`
	MaxTokens int    `json:"max_tokens,omitempty"`
}

type TranscriptSegment struct {
	ID        string  `json:"id,omitempty"`
	Speaker   string  `json:"speaker,omitempty"`
	Text      string  `json:"text"`
	StartTime float64 `json:"start_time,omitempty"`
	EndTime   float64 `json:"end_time,omitempty"`
}

type Source struct {
	Kind      string  `json:"kind"`
	ID        string  `json:"id,omitempty"`
	Label     string  `json:"label,omitempty"`
	StartTime float64 `json:"start_time,omitempty"`
	EndTime   float64 `json:"end_time,omitempty"`
}

type ContextResult struct {
	Text            string   `json:"text"`
	EffectiveMode   string   `json:"effective_mode"`
	EstimatedTokens int      `json:"estimated_tokens"`
	Truncated       bool     `json:"truncated"`
	Sources         []Source `json:"sources,omitempty"`
}

func MaxContextTokens() int {
	configuredMax := DefaultMaxTokens
	value := strings.TrimSpace(os.Getenv("AI_MAX_CONTEXT_TOKENS"))
	if value != "" {
		if parsed, err := strconv.Atoi(value); err == nil && parsed >= 1_024 {
			configuredMax = parsed
		}
	}
	outputReserve := DefaultOutputReserve
	if value := strings.TrimSpace(os.Getenv("AI_CONTEXT_OUTPUT_RESERVE_TOKENS")); value != "" {
		if parsed, err := strconv.Atoi(value); err == nil && parsed >= 0 {
			outputReserve = parsed
		}
	}
	modelWindow := int(^uint(0) >> 1)
	if configuredMax <= modelWindow-outputReserve {
		modelWindow = configuredMax + outputReserve
	}
	if value := strings.TrimSpace(os.Getenv("AI_MODEL_CONTEXT_WINDOW_TOKENS")); value != "" {
		if parsed, err := strconv.Atoi(value); err == nil && parsed >= 1_024 {
			modelWindow = parsed
		}
	}
	availableInput := modelWindow - outputReserve
	if availableInput < 1 {
		availableInput = 1
	}
	if availableInput < configuredMax {
		return availableInput
	}
	return configuredMax
}

func NormalizePolicy(policy ContextPolicy) (ContextPolicy, error) {
	policy.Mode = strings.ToLower(strings.TrimSpace(policy.Mode))
	if policy.Mode == "" {
		policy.Mode = "smart"
	}
	switch policy.Mode {
	case "smart", "full", "retrieval":
	default:
		return ContextPolicy{}, fmt.Errorf("unsupported context mode %q", policy.Mode)
	}
	hardMax := MaxContextTokens()
	if policy.MaxTokens <= 0 {
		policy.MaxTokens = DefaultContextTokens
	}
	if policy.MaxTokens > hardMax {
		policy.MaxTokens = hardMax
	}
	return policy, nil
}

// EstimateTokens is a tokenizer-independent upper bound: a provider token
// cannot represent fewer than one input byte. It intentionally overestimates
// common BPE tokenizers so max_context_tokens is an enforcement boundary, not
// an optimistic display estimate that can overflow on CJK, markup, or unusual
// compatible-provider tokenizers.
func EstimateTokens(text string) int {
	return len(text)
}

func FormatTranscript(segments []TranscriptSegment) string {
	var builder strings.Builder
	for _, segment := range segments {
		line := formatTranscriptSegment(segment)
		if line == "" {
			continue
		}
		if builder.Len() > 0 {
			builder.WriteByte('\n')
		}
		builder.WriteString(line)
	}
	return builder.String()
}

func formatTranscriptSegment(segment TranscriptSegment) string {
	text := strings.Join(strings.Fields(strings.TrimSpace(segment.Text)), " ")
	if text == "" {
		return ""
	}
	speaker := strings.TrimSpace(segment.Speaker)
	if speaker == "" {
		speaker = "Speaker"
	}
	if segment.EndTime > segment.StartTime {
		return fmt.Sprintf(
			"[%.1f–%.1f] %s: %s",
			segment.StartTime,
			segment.EndTime,
			speaker,
			text,
		)
	}
	return fmt.Sprintf("%s: %s", speaker, text)
}

// measureTranscript formats at most one segment at a time and stops as soon as
// the rendered transcript is known to exceed maxBytes. The returned byte count
// includes the first overflowing complete segment, so callers can still report
// a conservative lower-bound estimate without materializing the full input.
func measureTranscript(
	segments []TranscriptSegment,
	maxBytes int,
) (renderedBytes int, nonEmpty int, exceeded bool) {
	for _, segment := range segments {
		line := formatTranscriptSegment(segment)
		if line == "" {
			continue
		}
		if nonEmpty > 0 {
			renderedBytes++
		}
		renderedBytes += len(line)
		nonEmpty++
		if renderedBytes > maxBytes {
			return renderedBytes, nonEmpty, true
		}
	}
	return renderedBytes, nonEmpty, false
}

func fitNewestTranscript(
	segments []TranscriptSegment,
	maxBytes int,
) (selected []TranscriptSegment, dropped bool) {
	var reversed []TranscriptSegment
	renderedBytes := 0
	for index := len(segments) - 1; index >= 0; index-- {
		line := formatTranscriptSegment(segments[index])
		if line == "" {
			continue
		}
		addedBytes := len(line)
		if len(reversed) > 0 {
			addedBytes++
		}
		if renderedBytes+addedBytes > maxBytes {
			dropped = true
			break
		}
		renderedBytes += addedBytes
		reversed = append(reversed, segments[index])
	}
	for left, right := 0, len(reversed)-1; left < right; left, right = left+1, right-1 {
		reversed[left], reversed[right] = reversed[right], reversed[left]
	}
	return reversed, dropped
}

// ResolveTranscript applies the user-visible context policy. Smart mode sends
// the full transcript when it fits, otherwise keeps the newest complete lines.
// Retrieval mode is intentionally transcript-free; the caller supplies indexed
// excerpts so this package never silently pretends retrieval occurred.
func ResolveTranscript(segments []TranscriptSegment, requested ContextPolicy) (ContextResult, error) {
	policy, err := NormalizePolicy(requested)
	if err != nil {
		return ContextResult{}, err
	}
	if policy.Mode == "retrieval" {
		return ContextResult{EffectiveMode: "retrieval"}, nil
	}
	tokens, _, exceeded := measureTranscript(segments, policy.MaxTokens)
	if policy.Mode == "full" {
		if exceeded {
			return ContextResult{}, fmt.Errorf("%w: estimated %d tokens, limit %d", ErrContextTooLarge, tokens, policy.MaxTokens)
		}
		full := FormatTranscript(segments)
		return ContextResult{
			Text:            full,
			EffectiveMode:   "full",
			EstimatedTokens: tokens,
			Sources:         transcriptSources(segments),
		}, nil
	}
	if !exceeded {
		full := FormatTranscript(segments)
		return ContextResult{
			Text:            full,
			EffectiveMode:   "full",
			EstimatedTokens: tokens,
			Sources:         transcriptSources(segments),
		}, nil
	}

	selected, _ := fitNewestTranscript(segments, policy.MaxTokens)
	text := FormatTranscript(selected)
	return ContextResult{
		Text:            text,
		EffectiveMode:   "smart",
		EstimatedTokens: EstimateTokens(text),
		Truncated:       true,
		Sources:         transcriptSources(selected),
	}, nil
}

func transcriptSources(segments []TranscriptSegment) []Source {
	if len(segments) == 0 {
		return nil
	}
	first := segments[0]
	last := segments[len(segments)-1]
	return []Source{{
		Kind:      "transcript",
		ID:        first.ID,
		Label:     fmt.Sprintf("%d transcript segments", len(segments)),
		StartTime: first.StartTime,
		EndTime:   last.EndTime,
	}}
}
