package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/dreamtrans/backend/internal/store"
)

// The stored concept map document. Content of a concept_map artifact is
// exactly one JSON-encoded conceptMapDocument, validated server-side; the
// model never writes it directly.
type conceptMapDocument struct {
	Version      int               `json:"version"`
	GeneratedAt  time.Time         `json:"generated_at"`
	SessionCount int               `json:"session_count"`
	Truncated    bool              `json:"truncated,omitempty"`
	Topics       []conceptMapTopic `json:"topics"`
	Links        []conceptMapLink  `json:"links"`
}

type conceptMapTopic struct {
	ID       string           `json:"id"`
	Label    string           `json:"label"`
	New      bool             `json:"new,omitempty"`
	Children []conceptMapNode `json:"children"`
}

type conceptMapNode struct {
	ID       string               `json:"id"`
	Label    string               `json:"label"`
	Summary  string               `json:"summary,omitempty"`
	New      bool                 `json:"new,omitempty"`
	Evidence []conceptMapEvidence `json:"evidence,omitempty"`
}

type conceptMapEvidence struct {
	SessionID    string `json:"session_id"`
	SessionTitle string `json:"session_title,omitempty"`
	Quote        string `json:"quote"`
}

type conceptMapLink struct {
	From  string `json:"from"`
	To    string `json:"to"`
	Label string `json:"label,omitempty"`
}

// The shape the model is instructed to emit. Links reference concept labels,
// not ids: the server assigns stable ids after validation.
type conceptMapLLMOutput struct {
	Topics []struct {
		Label    string `json:"label"`
		Children []struct {
			Label    string `json:"label"`
			Summary  string `json:"summary"`
			Evidence []struct {
				Session int    `json:"session"`
				Quote   string `json:"quote"`
			} `json:"evidence"`
		} `json:"children"`
	} `json:"topics"`
	Links []struct {
		From  string `json:"from"`
		To    string `json:"to"`
		Label string `json:"label"`
	} `json:"links"`
}

const (
	conceptMapMaxTopics          = 14
	conceptMapMaxChildrenPer     = 30
	conceptMapMaxConcepts        = 200
	conceptMapMaxLinks           = 30
	conceptMapMaxEvidencePerNode = 3
	conceptMapMaxLabelRunes      = 80
	conceptMapMaxSummaryRunes    = 400
	conceptMapMaxQuoteRunes      = 160
	conceptMapMaxLinkLabelRunes  = 40
)

var errConceptMapInvalidJSON = errors.New("concept map generation returned invalid JSON")

// parseGeneratedConceptMap tolerates Markdown code fences and leading prose
// around the JSON object, but requires one decodable object with topics.
func parseGeneratedConceptMap(content string) (*conceptMapLLMOutput, error) {
	trimmed := strings.TrimSpace(content)
	start := strings.Index(trimmed, "{")
	end := strings.LastIndex(trimmed, "}")
	if start < 0 || end <= start {
		return nil, errConceptMapInvalidJSON
	}
	var out conceptMapLLMOutput
	if err := json.Unmarshal([]byte(trimmed[start:end+1]), &out); err != nil {
		return nil, errConceptMapInvalidJSON
	}
	if len(out.Topics) == 0 {
		return nil, errConceptMapInvalidJSON
	}
	return &out, nil
}

func clampConceptMapText(value string, maxRunes int) string {
	value = strings.Join(strings.Fields(value), " ")
	runes := []rune(value)
	if len(runes) > maxRunes {
		return strings.TrimSpace(string(runes[:maxRunes]))
	}
	return value
}

func conceptMapLabelKey(label string) string {
	return strings.ToLower(strings.Join(strings.Fields(label), " "))
}

// conceptMapLabelSet flattens every topic and concept label of a previous
// document so a regenerated map can mark genuinely new nodes.
func conceptMapLabelSet(doc *conceptMapDocument) map[string]bool {
	if doc == nil {
		return nil
	}
	labels := make(map[string]bool)
	for _, topic := range doc.Topics {
		labels[conceptMapLabelKey(topic.Label)] = true
		for _, child := range topic.Children {
			labels[conceptMapLabelKey(child.Label)] = true
		}
	}
	return labels
}

// buildConceptMapDocument validates and bounds raw model output, resolves
// evidence session ordinals to real sessions, assigns stable ids, resolves
// label-based links to ids, and marks nodes absent from the previous map.
// A nil previous document marks nothing as new (a first map where everything
// glows "new" is noise, not signal).
func buildConceptMapDocument(
	raw *conceptMapLLMOutput,
	sessions []store.ProjectSessionRef,
	previous *conceptMapDocument,
) *conceptMapDocument {
	previousLabels := conceptMapLabelSet(previous)
	doc := &conceptMapDocument{
		Version:      1,
		SessionCount: len(sessions),
		Topics:       make([]conceptMapTopic, 0, len(raw.Topics)),
		Links:        make([]conceptMapLink, 0, len(raw.Links)),
	}
	labelToID := make(map[string]string)
	totalConcepts := 0
	seenTopics := make(map[string]bool)
	for _, rawTopic := range raw.Topics {
		if len(doc.Topics) >= conceptMapMaxTopics || totalConcepts >= conceptMapMaxConcepts {
			break
		}
		label := clampConceptMapText(rawTopic.Label, conceptMapMaxLabelRunes)
		if label == "" || seenTopics[conceptMapLabelKey(label)] {
			continue
		}
		seenTopics[conceptMapLabelKey(label)] = true
		topic := conceptMapTopic{
			ID:       fmt.Sprintf("t%d", len(doc.Topics)+1),
			Label:    label,
			New:      previousLabels != nil && !previousLabels[conceptMapLabelKey(label)],
			Children: make([]conceptMapNode, 0, len(rawTopic.Children)),
		}
		seenChildren := make(map[string]bool)
		for _, rawChild := range rawTopic.Children {
			if len(topic.Children) >= conceptMapMaxChildrenPer ||
				totalConcepts >= conceptMapMaxConcepts {
				break
			}
			childLabel := clampConceptMapText(rawChild.Label, conceptMapMaxLabelRunes)
			key := conceptMapLabelKey(childLabel)
			if childLabel == "" || seenChildren[key] {
				continue
			}
			seenChildren[key] = true
			node := conceptMapNode{
				ID:      fmt.Sprintf("%s-c%d", topic.ID, len(topic.Children)+1),
				Label:   childLabel,
				Summary: clampConceptMapText(rawChild.Summary, conceptMapMaxSummaryRunes),
				New:     previousLabels != nil && !previousLabels[key],
			}
			for _, rawEvidence := range rawChild.Evidence {
				if len(node.Evidence) >= conceptMapMaxEvidencePerNode {
					break
				}
				quote := clampConceptMapText(rawEvidence.Quote, conceptMapMaxQuoteRunes)
				ordinal := rawEvidence.Session
				if quote == "" || ordinal < 1 || ordinal > len(sessions) {
					continue
				}
				session := sessions[ordinal-1]
				node.Evidence = append(node.Evidence, conceptMapEvidence{
					SessionID:    session.ID,
					SessionTitle: session.Title,
					Quote:        quote,
				})
			}
			if _, exists := labelToID[key]; !exists {
				labelToID[key] = node.ID
			}
			topic.Children = append(topic.Children, node)
			totalConcepts++
		}
		doc.Topics = append(doc.Topics, topic)
	}
	seenLinks := make(map[string]bool)
	for _, rawLink := range raw.Links {
		if len(doc.Links) >= conceptMapMaxLinks {
			break
		}
		fromID := labelToID[conceptMapLabelKey(rawLink.From)]
		toID := labelToID[conceptMapLabelKey(rawLink.To)]
		if fromID == "" || toID == "" || fromID == toID {
			continue
		}
		pairKey := fromID + ">" + toID
		reverseKey := toID + ">" + fromID
		if seenLinks[pairKey] || seenLinks[reverseKey] {
			continue
		}
		seenLinks[pairKey] = true
		doc.Links = append(doc.Links, conceptMapLink{
			From:  fromID,
			To:    toID,
			Label: clampConceptMapText(rawLink.Label, conceptMapMaxLinkLabelRunes),
		})
	}
	return doc
}

// parseStoredConceptMap decodes a persisted concept_map artifact body. It
// returns nil for content this server version cannot render rather than
// failing the request: the artifact stays retrievable and regenerable.
func parseStoredConceptMap(content string) *conceptMapDocument {
	var doc conceptMapDocument
	if err := json.Unmarshal([]byte(content), &doc); err != nil {
		return nil
	}
	if doc.Version < 1 || len(doc.Topics) == 0 {
		return nil
	}
	return &doc
}

// conceptMapSkeleton renders the previous map as an indented label outline the
// model can extend, bounded so an old map never crowds out new transcripts.
func conceptMapSkeleton(doc *conceptMapDocument) string {
	if doc == nil {
		return ""
	}
	const maxRunes = 4000
	var builder strings.Builder
	for _, topic := range doc.Topics {
		builder.WriteString("- 主题：" + topic.Label + "\n")
		for _, child := range topic.Children {
			builder.WriteString("  - " + child.Label + "\n")
		}
		if len([]rune(builder.String())) > maxRunes {
			break
		}
	}
	skeleton := strings.TrimRight(builder.String(), "\n")
	runes := []rune(skeleton)
	if len(runes) > maxRunes {
		skeleton = string(runes[:maxRunes])
	}
	return skeleton
}

func conceptMapInstruction(previous *conceptMapDocument) string {
	var builder strings.Builder
	builder.WriteString(`你是知识地图整理助手。请把下面按编号给出的多场会话转录，整理成一张用于学习复习的层级式概念地图，只输出一个严格合法的 JSON 对象（不要 Markdown 代码块，不要任何解释文字）。

JSON 结构：
{"topics":[{"label":"主题名","children":[{"label":"概念名","summary":"一到两句中文解释","evidence":[{"session":1,"quote":"该会话转录中的原文短句"}]}]}],"links":[{"from":"概念名A","to":"概念名B","label":"两者的关系"}]}

要求：
- 主题不超过 10 个，每个主题下概念不超过 20 个；只收录转录里真正出现的内容，不要杜撰。
- 概念名要简短（20 字以内）；summary 用中文，不超过两句。
- evidence 的 session 必须是转录前标注的会话编号；quote 必须摘自该会话转录原文，40 字以内；每个概念最多 2 条 evidence。
- links 只保留最重要的跨主题关联，不超过 10 条；from/to 必须是 children 里已出现的概念名；label 不超过 10 个字。`)
	if skeleton := conceptMapSkeleton(previous); skeleton != "" {
		builder.WriteString("\n\n下面是上一版地图的结构。请在它的基础上延续：仍然成立的主题名和概念名保持原样，把新内容融入进去；转录中已不成立或从未出现的条目可以删去。\n")
		builder.WriteString(skeleton)
	}
	return builder.String()
}
