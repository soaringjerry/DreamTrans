// Package ai contains provider-independent AI context assembly.
package ai

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"unicode"
)

const (
	DefaultContextTokens = 64_000
	DefaultMaxTokens     = 256_000
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
	value := strings.TrimSpace(os.Getenv("AI_MAX_CONTEXT_TOKENS"))
	if value == "" {
		return DefaultMaxTokens
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < 1_024 {
		return DefaultMaxTokens
	}
	return parsed
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

// EstimateTokens is deliberately conservative for mixed Latin/CJK transcripts.
// It is a budget guard, not a replacement for a model-specific tokenizer.
func EstimateTokens(text string) int {
	if text == "" {
		return 0
	}
	var latin, nonLatin int
	for _, r := range text {
		if unicode.IsSpace(r) {
			continue
		}
		if r <= unicode.MaxASCII {
			latin++
		} else {
			nonLatin++
		}
	}
	return (latin+3)/4 + (nonLatin+1)/2
}

func FormatTranscript(segments []TranscriptSegment) string {
	var builder strings.Builder
	for _, segment := range segments {
		text := strings.Join(strings.Fields(strings.TrimSpace(segment.Text)), " ")
		if text == "" {
			continue
		}
		if builder.Len() > 0 {
			builder.WriteByte('\n')
		}
		speaker := strings.TrimSpace(segment.Speaker)
		if speaker == "" {
			speaker = "Speaker"
		}
		if segment.EndTime > segment.StartTime {
			_, _ = fmt.Fprintf(&builder, "[%.1f–%.1f] %s: %s", segment.StartTime, segment.EndTime, speaker, text)
		} else {
			_, _ = fmt.Fprintf(&builder, "%s: %s", speaker, text)
		}
	}
	return builder.String()
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
	full := FormatTranscript(segments)
	tokens := EstimateTokens(full)
	if policy.Mode == "full" {
		if tokens > policy.MaxTokens {
			return ContextResult{}, fmt.Errorf("%w: estimated %d tokens, limit %d", ErrContextTooLarge, tokens, policy.MaxTokens)
		}
		return ContextResult{
			Text:            full,
			EffectiveMode:   "full",
			EstimatedTokens: tokens,
			Sources:         transcriptSources(segments),
		}, nil
	}
	if tokens <= policy.MaxTokens {
		return ContextResult{
			Text:            full,
			EffectiveMode:   "full",
			EstimatedTokens: tokens,
			Sources:         transcriptSources(segments),
		}, nil
	}

	selected := make([]TranscriptSegment, 0, len(segments))
	used := 0
	for index := len(segments) - 1; index >= 0; index-- {
		lineTokens := EstimateTokens(FormatTranscript(segments[index : index+1]))
		if used+lineTokens > policy.MaxTokens {
			if len(selected) == 0 {
				segment := segments[index]
				segment.Text = fitNewestSegment(segment, policy.MaxTokens)
				if strings.TrimSpace(segment.Text) != "" {
					selected = append(selected, segment)
				}
			}
			break
		}
		selected = append(selected, segments[index])
		used += lineTokens
	}
	for left, right := 0, len(selected)-1; left < right; left, right = left+1, right-1 {
		selected[left], selected[right] = selected[right], selected[left]
	}
	text := FormatTranscript(selected)
	return ContextResult{
		Text:            text,
		EffectiveMode:   "smart",
		EstimatedTokens: EstimateTokens(text),
		Truncated:       true,
		Sources:         transcriptSources(selected),
	}, nil
}

func fitNewestSegment(segment TranscriptSegment, maxTokens int) string {
	runes := []rune(strings.TrimSpace(segment.Text))
	if EstimateTokens(FormatTranscript([]TranscriptSegment{segment})) <= maxTokens {
		return string(runes)
	}
	left, right := 0, len(runes)
	for left < right {
		middle := (left + right + 1) / 2
		candidate := string(runes[len(runes)-middle:])
		segment.Text = candidate
		if EstimateTokens(FormatTranscript([]TranscriptSegment{segment})) <= maxTokens {
			left = middle
		} else {
			right = middle - 1
		}
	}
	if left == 0 {
		return ""
	}
	return string(runes[len(runes)-left:])
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
