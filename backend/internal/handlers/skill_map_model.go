package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	aicontext "github.com/dreamtrans/backend/internal/ai"
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
	skillMapMaxSkills                = 16
	skillMapMaxPrerequisitesPer      = 3
	skillMapMaxEvidencePerSkill      = 2
	skillMapMaxLabelRunes            = 60
	skillMapMaxSummaryRunes          = 300
	skillMapMaxOutcomeRunes          = 200
	skillMapMaxQuoteRunes            = 160
	skillMapSkeletonMaxRunes         = 2000
	skillMapTranscriptPageSize       = 500
	skillMapChunkInstructionOverhead = 8_000
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

func skillMapChunkInstruction() string {
	return `你是课程技能地图整理助手。下面是一门课的一部分课堂转录（完整一场，或一场里按时间切出的连续一段，标了「续」的是同一场的后续）。请只根据这部分真正教过的内容提炼技能，不要因为这不是全文就省略后半段里出现的能力。只输出一个严格合法的 JSON 对象（不要 Markdown 代码块，不要任何解释文字）。

JSON 结构：
{"skills":[{"label":"能力名","summary":"一到两句中文说明这项能力是什么","outcome":"以「能」开头的一句可观察行为描述","prerequisites":["它直接依赖的能力名"],"evidence":[{"session":1,"quote":"该会话转录中的原文短句"}]}]}

要求：
- 技能 3~8 项，按这部分内容从基础到进阶排序；只提炼这段转录里真正教过的能力，不要杜撰。
- label 是能力而不是章节名（如「区分相关与因果」，不是「第三讲」），20 字以内。
- outcome 必须是可观察、可考核的行为（能判断/能指出/能设计……），一句话。
- prerequisites 只能引用本段列表中排在它前面的能力名，最多 3 个；没有就给空数组。
- evidence 的 session 必须是文本里 [会话 N] 的编号；quote 摘自该段原文，40 字以内；每项能力最多 2 条。`
}

func skillMapMergeInstruction(previous *skillMapDocument) string {
	var builder strings.Builder
	builder.WriteString(`你是课程技能地图整理助手。下面是从同一门课各场/各段转录分别提炼出的技能草稿。请合并成一张完整技能地图，覆盖草稿里出现过的全部教学内容，不要因为合并而丢掉后半段课才出现的能力。只输出一个严格合法的 JSON 对象（不要 Markdown 代码块，不要任何解释文字）。

JSON 结构：
{"skills":[{"label":"能力名","summary":"一到两句中文说明这项能力是什么","outcome":"以「能」开头的一句可观察行为描述","prerequisites":["它直接依赖的能力名"],"evidence":[{"session":1,"quote":"课堂原文短句"}]}]}

要求：
- 技能 6~12 项，按从基础到进阶排序；近义名称合并为一项，保留更准确的 label。
- 只使用草稿里出现过的能力，不要杜撰新课没教的内容。
- outcome 必须是可观察、可考核的行为（能判断/能指出/能设计……），一句话。
- prerequisites 只能引用合并后列表中排在它前面的能力名，最多 3 个；没有就给空数组。
- evidence 保留草稿里最能代表课堂原文的 quote，session 编号保持不变；每项最多 2 条。`)
	if skeleton := skillMapSkeleton(previous); skeleton != "" {
		builder.WriteString("\n\n下面是上一版技能地图的顺序。请在它的基础上延续：仍然成立的能力名保持原样和相对顺序。\n")
		builder.WriteString(skeleton)
	}
	return builder.String()
}

func skillMapTranscriptBudget(maxContextTokens int) int {
	budget := maxContextTokens
	if systemMax := aicontext.MaxContextTokens(); budget <= 0 || budget > systemMax {
		budget = systemMax
	}
	if budget <= skillMapChunkInstructionOverhead+1024 {
		if budget < 1024 {
			return 1024
		}
		return budget / 2
	}
	return budget - skillMapChunkInstructionOverhead
}

func formatSkillMapSessionBlock(index int, session store.ProjectSessionRef, body, continuation string) string {
	title := strings.TrimSpace(session.Title)
	if title == "" {
		title = "未命名会话"
	}
	header := fmt.Sprintf("[会话 %d] %s（%s）", index+1, title, session.StartedAt.Format("2006-01-02"))
	if continuation != "" {
		header += continuation
	}
	return header + "\n" + body
}

// splitTextToBudget covers the whole string with consecutive pieces that each
// fit the byte budget. It never drops a suffix: leftover bytes become the
// next piece. A single oversize rune is emitted alone rather than skipped.
func splitTextToBudget(text string, budget int) []string {
	if budget < 1 {
		budget = 1
	}
	if text == "" {
		return nil
	}
	if aicontext.EstimateTokens(text) <= budget {
		return []string{text}
	}
	pieces := make([]string, 0)
	start := 0
	bytes := 0
	for index, r := range text {
		runeLen := len(string(r))
		if bytes > 0 && bytes+runeLen > budget {
			pieces = append(pieces, text[start:index])
			start = index
			bytes = 0
		}
		if runeLen > budget && bytes == 0 {
			end := index + runeLen
			pieces = append(pieces, text[index:end])
			start = end
			bytes = 0
			continue
		}
		bytes += runeLen
	}
	if start < len(text) {
		pieces = append(pieces, text[start:])
	}
	return pieces
}

// packSkillMapChunks groups session transcripts into model-sized pieces
// without dropping any confirmed text. Sessions that fit stay whole; a
// runaway session is split in time order, each piece still labeled with the
// same [会话 N] ordinal.
func packSkillMapChunks(
	sessions []store.ProjectSessionRef,
	texts []string,
	budget int,
) []string {
	if budget < 1 {
		budget = 1
	}
	chunks := make([]string, 0)
	var current strings.Builder
	currentTokens := 0
	flush := func() {
		if current.Len() == 0 {
			return
		}
		chunks = append(chunks, strings.TrimSpace(current.String()))
		current.Reset()
		currentTokens = 0
	}
	appendFit := func(block string) {
		need := aicontext.EstimateTokens(block)
		if current.Len() > 0 {
			if currentTokens+2+need <= budget {
				current.WriteString("\n\n")
				current.WriteString(block)
				currentTokens += 2 + need
				return
			}
			flush()
		}
		current.WriteString(block)
		currentTokens = need
	}
	for index, session := range sessions {
		body := strings.TrimSpace(texts[index])
		if body == "" {
			continue
		}
		block := formatSkillMapSessionBlock(index, session, body, "")
		if aicontext.EstimateTokens(block) <= budget {
			appendFit(block)
			continue
		}
		flush()
		headerBudget := aicontext.EstimateTokens(formatSkillMapSessionBlock(index, session, "x", "（续）"))
		pieceBudget := budget - headerBudget
		if pieceBudget < 1 {
			pieceBudget = 1
		}
		pieces := splitTextToBudget(body, pieceBudget)
		for pieceIndex, piece := range pieces {
			continuation := ""
			if pieceIndex > 0 {
				continuation = "（续）"
			}
			appendFit(formatSkillMapSessionBlock(index, session, piece, continuation))
			flush()
		}
	}
	flush()
	return chunks
}
