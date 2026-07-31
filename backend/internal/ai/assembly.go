package ai

import (
	"fmt"
	"strings"
)

const transcriptSectionPrefix = "[Transcript]\n"

// ContextBlock is an already-ranked piece of non-transcript context. Callers
// should pass blocks in descending relevance order. The assembler never splits
// a block, which keeps source attribution honest.
type ContextBlock struct {
	Text    string
	Section string
	Source  Source
}

// AssemblyInput describes every piece of model-readable input. FixedText is
// not copied into ContextResult.Text; it represents the system prompt, recent
// conversation history, and current question that are sent separately to the
// provider but still consume the same user-visible input budget.
type AssemblyInput struct {
	Policy     ContextPolicy
	FixedText  string
	Transcript []TranscriptSegment
	Blocks     []ContextBlock
}

// Assemble applies one total input budget across fixed prompt material,
// transcript text, and ranked retrieval blocks.
//
// Full mode is strict and never truncates. Retrieval mode never includes a
// transcript dump. Smart mode sends everything when it fits; otherwise it
// keeps ranked retrieval blocks first and fills the remainder with the newest
// complete transcript segments.
func Assemble(input AssemblyInput) (ContextResult, error) {
	policy, err := NormalizePolicy(input.Policy)
	if err != nil {
		return ContextResult{}, err
	}
	fixedTokens := EstimateTokens(input.FixedText)
	if fixedTokens > policy.MaxTokens {
		return ContextResult{}, fmt.Errorf(
			"%w: fixed prompt/history/question require an estimated %d tokens, limit %d",
			ErrContextTooLarge,
			fixedTokens,
			policy.MaxTokens,
		)
	}

	blocks := normalizeBlocks(input.Blocks)
	remaining := policy.MaxTokens - fixedTokens

	switch policy.Mode {
	case "retrieval":
		selected, sources, dropped, renderedBytes := fitBlocks(blocks, remaining)
		return ContextResult{
			Text:            formatBlocks(selected),
			EffectiveMode:   "retrieval",
			EstimatedTokens: fixedTokens + renderedBytes,
			Truncated:       dropped,
			Sources:         sources,
		}, nil
	case "full":
		blockBytes, blocksExceeded := measureBlocks(blocks, remaining)
		if blocksExceeded {
			return ContextResult{}, fmt.Errorf(
				"%w: estimated %d total input tokens, limit %d",
				ErrContextTooLarge,
				fixedTokens+blockBytes,
				policy.MaxTokens,
			)
		}
		transcriptBudget := transcriptBudgetForBlocks(remaining, blockBytes)
		transcriptBytes, transcriptCount, transcriptExceeded := measureTranscript(
			input.Transcript,
			transcriptBudget,
		)
		if transcriptExceeded {
			return ContextResult{}, fmt.Errorf(
				"%w: estimated %d total input tokens, limit %d",
				ErrContextTooLarge,
				fixedTokens+contextBytes(transcriptBytes, transcriptCount > 0, blockBytes),
				policy.MaxTokens,
			)
		}
		fullTranscript := FormatTranscript(input.Transcript)
		allContext, allSources := joinContext(fullTranscript, input.Transcript, blocks)
		return ContextResult{
			Text:          allContext,
			EffectiveMode: "full",
			EstimatedTokens: fixedTokens + contextBytes(
				transcriptBytes,
				transcriptCount > 0,
				blockBytes,
			),
			Sources: allSources,
		}, nil
	}

	blockBytes, blocksExceeded := measureBlocks(blocks, remaining)
	if !blocksExceeded {
		transcriptBudget := transcriptBudgetForBlocks(remaining, blockBytes)
		transcriptBytes, transcriptCount, transcriptExceeded := measureTranscript(
			input.Transcript,
			transcriptBudget,
		)
		if !transcriptExceeded {
			fullTranscript := FormatTranscript(input.Transcript)
			allContext, allSources := joinContext(fullTranscript, input.Transcript, blocks)
			return ContextResult{
				Text:          allContext,
				EffectiveMode: "full",
				EstimatedTokens: fixedTokens + contextBytes(
					transcriptBytes,
					transcriptCount > 0,
					blockBytes,
				),
				Sources: allSources,
			}, nil
		}
	}

	selectedBlocks, _, blocksDropped, selectedBlockBytes := fitBlocks(blocks, remaining)
	selectedTranscript, transcriptDropped := fitNewestTranscript(
		input.Transcript,
		transcriptBudgetForBlocks(remaining, selectedBlockBytes),
	)
	text, transcriptAndBlockSources := joinContext(
		FormatTranscript(selectedTranscript),
		selectedTranscript,
		selectedBlocks,
	)
	return ContextResult{
		Text:            text,
		EffectiveMode:   "smart",
		EstimatedTokens: fixedTokens + EstimateTokens(text),
		Truncated:       blocksDropped || transcriptDropped,
		Sources:         transcriptAndBlockSources,
	}, nil
}

func normalizeBlocks(blocks []ContextBlock) []ContextBlock {
	result := make([]ContextBlock, 0, len(blocks))
	for _, block := range blocks {
		block.Text = strings.TrimSpace(block.Text)
		block.Section = strings.TrimSpace(block.Section)
		if block.Text == "" {
			continue
		}
		result = append(result, block)
	}
	return result
}

func fitBlocks(
	blocks []ContextBlock,
	maxTokens int,
) ([]ContextBlock, []Source, bool, int) {
	if maxTokens <= 0 {
		return nil, nil, len(blocks) > 0, 0
	}
	var selected []ContextBlock
	var sources []Source
	renderedBytes := 0
	currentSection := ""
	for _, block := range blocks {
		addedBytes := blockAppendBytes(block, len(selected) > 0, currentSection)
		if renderedBytes+addedBytes > maxTokens {
			continue
		}
		selected = append(selected, block)
		sources = append(sources, block.Source)
		renderedBytes += addedBytes
		currentSection = block.Section
	}
	return selected, sources, len(selected) < len(blocks), renderedBytes
}

func measureBlocks(blocks []ContextBlock, maxBytes int) (renderedBytes int, exceeded bool) {
	currentSection := ""
	for index, block := range blocks {
		renderedBytes += blockAppendBytes(block, index > 0, currentSection)
		if renderedBytes > maxBytes {
			return renderedBytes, true
		}
		currentSection = block.Section
	}
	return renderedBytes, false
}

func blockAppendBytes(block ContextBlock, hasPrevious bool, currentSection string) int {
	renderedBytes := len(block.Text)
	if block.Section != currentSection {
		if hasPrevious {
			renderedBytes += 2
		}
		if block.Section != "" {
			renderedBytes += len(block.Section) + 3
		}
	} else if hasPrevious {
		renderedBytes++
	}
	return renderedBytes
}

func transcriptBudgetForBlocks(remaining int, blockBytes int) int {
	budget := remaining - blockBytes - len(transcriptSectionPrefix)
	if blockBytes > 0 {
		budget -= 2
	}
	return budget
}

func contextBytes(transcriptBytes int, hasTranscript bool, blockBytes int) int {
	if !hasTranscript {
		return blockBytes
	}
	renderedBytes := len(transcriptSectionPrefix) + transcriptBytes + blockBytes
	if blockBytes > 0 {
		renderedBytes += 2
	}
	return renderedBytes
}

func joinContext(
	transcript string,
	segments []TranscriptSegment,
	blocks []ContextBlock,
) (string, []Source) {
	var sections []string
	sources := make([]Source, 0, 1+len(blocks))
	if strings.TrimSpace(transcript) != "" {
		sections = append(sections, "[Transcript]\n"+strings.TrimSpace(transcript))
		sources = append(sources, transcriptSources(segments)...)
	}
	blockText := formatBlocks(blocks)
	if blockText != "" {
		sections = append(sections, blockText)
		for _, block := range blocks {
			sources = append(sources, block.Source)
		}
	}
	return strings.Join(sections, "\n\n"), sources
}

func formatBlocks(blocks []ContextBlock) string {
	if len(blocks) == 0 {
		return ""
	}
	var builder strings.Builder
	currentSection := ""
	for _, block := range blocks {
		if block.Section != currentSection {
			if builder.Len() > 0 {
				builder.WriteString("\n\n")
			}
			currentSection = block.Section
			if currentSection != "" {
				builder.WriteString("[")
				builder.WriteString(currentSection)
				builder.WriteString("]\n")
			}
		} else if builder.Len() > 0 {
			builder.WriteByte('\n')
		}
		builder.WriteString(block.Text)
	}
	return strings.TrimSpace(builder.String())
}
