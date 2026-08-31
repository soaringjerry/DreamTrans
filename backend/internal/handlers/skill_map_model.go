package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/dreamtrans/backend/internal/store"
)

// The stored skill map document (学习模式). Content of a skill_map artifact is
// exactly one JSON-encoded skillMapDocument, validated server-side; the model
// never writes it directly. Skills are ordered foundational → advanced and
// prerequisites only ever reference earlier skills, so the map is a DAG by
// construction. Learner progress lives outside this document.
type skillMapDocument struct {
	Version      int             `json:"version"`
	GeneratedAt  time.Time       `json:"generated_at"`
	SessionCount int             `json:"session_count"`
	Truncated    bool            `json:"truncated,omitempty"`
	Skills       []skillMapSkill `json:"skills"`
}

type skillMapSkill struct {
	ID      string `json:"id"`
	Label   string `json:"label"`
	Summary string `json:"summary,omitempty"`
	// Observable behavior ("能……") that shows the skill is held.
	Outcome string `json:"outcome,omitempty"`
	// IDs of earlier skills this one builds on.
	Prerequisites []string           `json:"prerequisites,omitempty"`
	New           bool               `json:"new,omitempty"`
	Evidence      []skillMapEvidence `json:"evidence,omitempty"`
}

type skillMapEvidence struct {
	SessionID    string `json:"session_id"`
	SessionTitle string `json:"session_title,omitempty"`
	Quote        string `json:"quote"`
}

// The shape the model is instructed to emit. Prerequisites reference skill
// labels, not ids: the server assigns stable ids after validation.
type skillMapLLMOutput struct {
	Skills []struct {
		Label         string   `json:"label"`
		Summary       string   `json:"summary"`
		Outcome       string   `json:"outcome"`
		Prerequisites []string `json:"prerequisites"`
		Evidence      []struct {
			Session int    `json:"session"`
			Quote   string `json:"quote"`
		} `json:"evidence"`
	} `json:"skills"`
}

const (
	skillMapMaxSkills             = 16
	skillMapMaxPrerequisitesPer   = 3
	skillMapMaxEvidencePerSkill   = 2
	skillMapMaxLabelRunes         = 60
	skillMapMaxSummaryRunes       = 300
	skillMapMaxOutcomeRunes       = 200
	skillMapMaxQuoteRunes         = 160
	skillMapSkeletonMaxRunes      = 2000
	skillMapContextSessionCap     = 40
	skillMapTranscriptPageSize    = 500
	skillMapGenerationTimeoutBase = 120 * time.Second
)

var errSkillMapInvalidJSON = errors.New("skill map generation returned invalid JSON")

// parseGeneratedSkillMap tolerates Markdown code fences and leading prose
// around the JSON object, but requires one decodable object with skills.
func parseGeneratedSkillMap(content string) (*skillMapLLMOutput, error) {
	trimmed := strings.TrimSpace(content)
	start := strings.Index(trimmed, "{")
	end := strings.LastIndex(trimmed, "}")
	if start < 0 || end <= start {
		return nil, errSkillMapInvalidJSON
	}
	var out skillMapLLMOutput
	if err := json.Unmarshal([]byte(trimmed[start:end+1]), &out); err != nil {
		return nil, errSkillMapInvalidJSON
	}
	if len(out.Skills) == 0 {
		return nil, errSkillMapInvalidJSON
	}
	return &out, nil
}

func clampSkillMapText(value string, maxRunes int) string {
	value = strings.Join(strings.Fields(value), " ")
	runes := []rune(value)
	if len(runes) > maxRunes {
		return strings.TrimSpace(string(runes[:maxRunes]))
	}
	return value
}

func skillMapLabelKey(label string) string {
	return strings.ToLower(strings.Join(strings.Fields(label), " "))
}

// skillMapLabelSet flattens every skill label of a previous document so a
// regenerated map can mark genuinely new skills.
func skillMapLabelSet(doc *skillMapDocument) map[string]bool {
	if doc == nil {
		return nil
	}
	labels := make(map[string]bool)
	for _, skill := range doc.Skills {
		labels[skillMapLabelKey(skill.Label)] = true
	}
	return labels
}

// buildSkillMapDocument validates and bounds raw model output, resolves
// evidence session ordinals to real sessions, assigns stable ids in emitted
// order, resolves prerequisite labels to the ids of EARLIER skills only
// (forward and self references are dropped, keeping the map acyclic), and
// marks skills absent from the previous map. A nil previous document marks
// nothing as new.
func buildSkillMapDocument(
	raw *skillMapLLMOutput,
	sessions []store.ProjectSessionRef,
	previous *skillMapDocument,
) *skillMapDocument {
	previousLabels := skillMapLabelSet(previous)
	doc := &skillMapDocument{
		Version:      1,
		SessionCount: len(sessions),
		Skills:       make([]skillMapSkill, 0, len(raw.Skills)),
	}
	labelToID := make(map[string]string)
	for _, rawSkill := range raw.Skills {
		if len(doc.Skills) >= skillMapMaxSkills {
			break
		}
		label := clampSkillMapText(rawSkill.Label, skillMapMaxLabelRunes)
		key := skillMapLabelKey(label)
		if label == "" || labelToID[key] != "" {
			continue
		}
		skill := skillMapSkill{
			ID:      fmt.Sprintf("s%d", len(doc.Skills)+1),
			Label:   label,
			Summary: clampSkillMapText(rawSkill.Summary, skillMapMaxSummaryRunes),
			Outcome: clampSkillMapText(rawSkill.Outcome, skillMapMaxOutcomeRunes),
			New:     previousLabels != nil && !previousLabels[key],
		}
		seenPrerequisites := make(map[string]bool)
		for _, rawPrerequisite := range rawSkill.Prerequisites {
			if len(skill.Prerequisites) >= skillMapMaxPrerequisitesPer {
				break
			}
			// Only earlier skills are in labelToID at this point, so forward
			// and self references resolve to "" and fall out here.
			prerequisiteID := labelToID[skillMapLabelKey(rawPrerequisite)]
			if prerequisiteID == "" || seenPrerequisites[prerequisiteID] {
				continue
			}
			seenPrerequisites[prerequisiteID] = true
			skill.Prerequisites = append(skill.Prerequisites, prerequisiteID)
		}
		for _, rawEvidence := range rawSkill.Evidence {
			if len(skill.Evidence) >= skillMapMaxEvidencePerSkill {
				break
			}
			quote := clampSkillMapText(rawEvidence.Quote, skillMapMaxQuoteRunes)
			ordinal := rawEvidence.Session
			if quote == "" || ordinal < 1 || ordinal > len(sessions) {
				continue
			}
			session := sessions[ordinal-1]
			skill.Evidence = append(skill.Evidence, skillMapEvidence{
				SessionID:    session.ID,
				SessionTitle: session.Title,
				Quote:        quote,
			})
		}
		labelToID[key] = skill.ID
		doc.Skills = append(doc.Skills, skill)
	}
	return doc
}

// parseStoredSkillMap decodes a persisted skill_map artifact body. It returns
// nil for content this server version cannot render rather than failing the
// request: the artifact stays retrievable and regenerable.
func parseStoredSkillMap(content string) *skillMapDocument {
	var doc skillMapDocument
	if err := json.Unmarshal([]byte(content), &doc); err != nil {
		return nil
	}
	if doc.Version < 1 || len(doc.Skills) == 0 {
		return nil
	}
	return &doc
}

// skillMapSkeleton renders the previous map as an ordered label outline the
// model can extend, bounded so an old map never crowds out new transcripts.
func skillMapSkeleton(doc *skillMapDocument) string {
	if doc == nil {
		return ""
	}
	var builder strings.Builder
	for index, skill := range doc.Skills {
		fmt.Fprintf(&builder, "%d. %s\n", index+1, skill.Label)
		if len([]rune(builder.String())) > skillMapSkeletonMaxRunes {
			break
		}
	}
	skeleton := strings.TrimRight(builder.String(), "\n")
	runes := []rune(skeleton)
	if len(runes) > skillMapSkeletonMaxRunes {
		skeleton = string(runes[:skillMapSkeletonMaxRunes])
	}
	return skeleton
}

func skillMapInstruction(previous *skillMapDocument) string {
	var builder strings.Builder
	builder.WriteString(`你是课程技能地图整理助手。请把下面按编号给出的多场课程会话转录，提炼成一张技能地图（Skill Map）：这门课要求学生掌握的一组能力，按从基础到进阶排序。只输出一个严格合法的 JSON 对象（不要 Markdown 代码块，不要任何解释文字）。

JSON 结构：
{"skills":[{"label":"能力名","summary":"一到两句中文说明这项能力是什么","outcome":"以「能」开头的一句可观察行为描述","prerequisites":["它直接依赖的能力名"],"evidence":[{"session":1,"quote":"该会话转录中的原文短句"}]}]}

要求：
- 技能 6~12 项，按从基础到进阶排序；只提炼转录里真正教过的能力，不要杜撰。
- label 是能力而不是章节名（如「区分相关与因果」，不是「第三讲」），20 字以内。
- outcome 必须是可观察、可考核的行为（能判断/能指出/能设计……），一句话。
- prerequisites 只能引用列表中排在它前面的能力名，最多 3 个；没有就给空数组。
- evidence 的 session 必须是转录前标注的会话编号；quote 摘自该会话转录原文，40 字以内；每项能力最多 2 条。`)
	if skeleton := skillMapSkeleton(previous); skeleton != "" {
		builder.WriteString("\n\n下面是上一版技能地图的顺序。请在它的基础上延续：仍然成立的能力名保持原样和相对顺序，把新内容融入进去；转录中已不成立或从未教过的条目可以删去。\n")
		builder.WriteString(skeleton)
	}
	return builder.String()
}
