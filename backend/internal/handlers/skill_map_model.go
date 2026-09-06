package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
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
	MaterialFingerprint string          `json:"material_fingerprint,omitempty"`
	Version             int             `json:"version"`
	GeneratedAt         time.Time       `json:"generated_at"`
	SessionCount        int             `json:"session_count"`
	SourceCount         int             `json:"source_count,omitempty"`
	Truncated           bool            `json:"truncated,omitempty"`
	Skills              []skillMapSkill `json:"skills"`
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

// Evidence points at either a linked session ([会话 N]) or an uploaded
// course material ([资料 N]); exactly one of the id pairs is set.
type skillMapEvidence struct {
	SessionID    string `json:"session_id,omitempty"`
	SessionTitle string `json:"session_title,omitempty"`
	SourceID     string `json:"source_id,omitempty"`
	SourceTitle  string `json:"source_title,omitempty"`
	Quote        string `json:"quote"`
}

// skillMapSourceRef is one ready uploaded material with its extracted text.
type skillMapSourceRef struct {
	ID    string
	Title string
	Text  string
}

// skillMapMaterial is one block of course text handed to the model, with
// the ordinal the model cites back as evidence.
type skillMapMaterial struct {
	Kind    string // "session" | "source"
	Ordinal int
	Title   string
	Date    string
	Body    string
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
			Source  int    `json:"source"`
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
	skillMapMaxChunkBytes            = 200_000
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
// order, resolves prerequisite labels across the whole map, removes cycles,
// sorts prerequisites before their dependents, and
// marks skills absent from the previous map. A nil previous document marks
// nothing as new.
func buildSkillMapDocument(
	raw *skillMapLLMOutput,
	sessions []store.ProjectSessionRef,
	sources []skillMapSourceRef,
	previous *skillMapDocument,
) *skillMapDocument {
	previousLabels := skillMapLabelSet(previous)
	doc := &skillMapDocument{
		Version:      1,
		SessionCount: len(sessions),
		SourceCount:  len(sources),
		Skills:       make([]skillMapSkill, 0, len(raw.Skills)),
	}
	labelToID := make(map[string]string)
	prerequisites := make(map[string][]string)
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
		prerequisites[skill.ID] = rawSkill.Prerequisites
		for _, rawEvidence := range rawSkill.Evidence {
			if len(skill.Evidence) >= skillMapMaxEvidencePerSkill {
				break
			}
			quote := clampSkillMapText(rawEvidence.Quote, skillMapMaxQuoteRunes)
			if quote == "" {
				continue
			}
			switch {
			case rawEvidence.Session >= 1 && rawEvidence.Session <= len(sessions):
				session := sessions[rawEvidence.Session-1]
				skill.Evidence = append(skill.Evidence, skillMapEvidence{
					SessionID:    session.ID,
					SessionTitle: session.Title,
					Quote:        quote,
				})
			case rawEvidence.Source >= 1 && rawEvidence.Source <= len(sources):
				source := sources[rawEvidence.Source-1]
				skill.Evidence = append(skill.Evidence, skillMapEvidence{
					SourceID:    source.ID,
					SourceTitle: source.Title,
					Quote:       quote,
				})
			}
		}
		labelToID[key] = skill.ID
		doc.Skills = append(doc.Skills, skill)
	}
	// Prefer edges already consistent with the emitted order when resolving
	// contradictory cycles, but do not discard valid forward references.
	indices := make(map[string]int)
	for i, skill := range doc.Skills {
		indices[skill.ID] = i
	}
	var reaches func(string, string) bool
	reaches = func(from, target string) bool {
		if from == target {
			return true
		}
		for _, parent := range doc.Skills[indices[from]].Prerequisites {
			if reaches(parent, target) {
				return true
			}
		}
		return false
	}
	for pass := 0; pass < 2; pass++ {
		for i := range doc.Skills {
			skill := &doc.Skills[i]
			for _, label := range prerequisites[skill.ID] {
				id := labelToID[skillMapLabelKey(clampSkillMapText(label, skillMapMaxLabelRunes))]
				if id == "" || (indices[id] < i) != (pass == 0) || len(skill.Prerequisites) >= skillMapMaxPrerequisitesPer {
					continue
				}
				duplicate := false
				for _, existing := range skill.Prerequisites {
					duplicate = duplicate || existing == id
				}
				if !duplicate && !reaches(id, skill.ID) {
					skill.Prerequisites = append(skill.Prerequisites, id)
				}
			}
		}
	}
	ordered := make([]skillMapSkill, 0, len(doc.Skills))
	visited := make(map[string]bool)
	var visit func(string)
	visit = func(id string) {
		if visited[id] {
			return
		}
		visited[id] = true
		skill := doc.Skills[indices[id]]
		for _, parent := range skill.Prerequisites {
			visit(parent)
		}
		ordered = append(ordered, skill)
	}
	for _, skill := range doc.Skills {
		visit(skill.ID)
	}
	doc.Skills = ordered
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

// skillMapSkeleton supplies only a bounded name vocabulary, without preserving
// the previous learning order or making the old map a source of evidence.
func skillMapSkeleton(doc *skillMapDocument) string {
	if doc == nil {
		return ""
	}
	var builder strings.Builder
	labels := make([]string, 0, len(doc.Skills))
	for _, skill := range doc.Skills {
		labels = append(labels, skill.Label)
	}
	sort.Strings(labels)
	for _, label := range labels {
		fmt.Fprintf(&builder, "- %s\n", label)
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

const skillMapRebuildInstruction = "\n\n以下旧能力名仅供名称匹配，不是教学证据，也不约束新图谱的顺序和结构。请先根据当前材料独立确定能力、粒度和依赖，允许重排、拆分、合并、增删；没有当前材料依据的旧能力必须删除。仅当能力含义和范围相同时沿用旧名称，以便关联已有学习记录；不能为了沿用名称保留错误结构或将不同能力强行合并。\n"

func skillMapInstruction(previous *skillMapDocument) string {
	var builder strings.Builder
	builder.WriteString(`你是课程技能地图整理助手。请根据下面按编号给出的当前全部课堂转录和上传资料，重新构建技能地图（Skill Map）：这门课要求学生掌握的一组能力，按真实前置依赖从基础到进阶排序。资料与转录都是有效依据；没有转录时也必须利用资料。只输出一个严格合法的 JSON 对象（不要 Markdown 代码块，不要任何解释文字）。

JSON 结构：
{"skills":[{"label":"能力名","summary":"一到两句中文说明这项能力是什么","outcome":"以「能」开头的一句可观察行为描述","prerequisites":["它直接依赖的能力名"],"evidence":[{"session":1,"quote":"该会话转录中的原文短句"},{"source":1,"quote":"该资料中的原文短句"}]}]}

要求：
- 技能通常 6~12 项，最多 16 项；根据当前材料的覆盖范围决定粒度，材料较少时可以更少，不要为凑数杜撰。
- label 是能力而不是章节名（如「区分相关与因果」，不是「第三讲」），20 字以内。
- outcome 必须是可观察、可考核的行为（能判断/能指出/能设计……），一句话。
- prerequisites 只能引用列表中排在它前面的能力名，最多 3 个；没有就给空数组。
- evidence 来自转录用 {"session":N}，来自上传资料用 {"source":N}，二选一且保留原始编号；quote 摘自对应原文，40 字以内；每项能力最多 2 条。`)
	if skeleton := skillMapSkeleton(previous); skeleton != "" {
		builder.WriteString(skillMapRebuildInstruction)
		builder.WriteString(skeleton)
	}
	return builder.String()
}

func skillMapChunkInstruction() string {
	return `你是课程技能地图整理助手。下面是一门课的一部分课堂转录或上传资料（标了「续」的是同一来源的后续）。两种来源都是有效依据；没有转录时也必须利用资料。请根据这部分内容提炼技能，不要省略后半段里出现的能力。只输出一个严格合法的 JSON 对象（不要 Markdown 代码块，不要任何解释文字）。

JSON 结构：
{"skills":[{"label":"能力名","summary":"一到两句中文说明这项能力是什么","outcome":"以「能」开头的一句可观察行为描述","prerequisites":["它直接依赖的能力名"],"evidence":[{"session":1,"quote":"该会话转录中的原文短句"}]}]}

要求：
- 技能通常 3~8 项，材料较少时可以更少，按这部分内容从基础到进阶排序；只提炼当前转录或资料中有依据的能力，不要杜撰。
- label 是能力而不是章节名（如「区分相关与因果」，不是「第三讲」），20 字以内。
- outcome 必须是可观察、可考核的行为（能判断/能指出/能设计……），一句话。
- prerequisites 只能引用本段列表中排在它前面的能力名，最多 3 个；没有就给空数组。
- evidence 引用来源：来自课堂转录用 {"session":N}（对应 [会话 N]），来自教材/课件/论文等资料用 {"source":N}（对应 [资料 N]），二选一；quote 摘自该段原文，40 字以内；每项能力最多 2 条。`
}

func skillMapMergeInstruction(previous *skillMapDocument) string {
	var builder strings.Builder
	builder.WriteString(`你是课程技能地图整理助手。下面是从同一门课当前课堂转录和上传资料分别提炼出的技能草稿。请重新构建完整技能地图，覆盖草稿里的教学内容，资料与转录同等有效，不要因为合并而丢掉后半段的能力。只输出一个严格合法的 JSON 对象（不要 Markdown 代码块，不要任何解释文字）。

JSON 结构：
{"skills":[{"label":"能力名","summary":"一到两句中文说明这项能力是什么","outcome":"以「能」开头的一句可观察行为描述","prerequisites":["它直接依赖的能力名"],"evidence":[{"session":1,"quote":"课堂原文短句"}]}]}

要求：
- 技能通常 6~12 项，最多 16 项，材料较少时可以更少；根据覆盖范围决定粒度，按真实前置依赖从基础到进阶排序；近义名称合并为一项。
- 只使用草稿里出现过的能力，不要杜撰新课没教的内容。
- outcome 必须是可观察、可考核的行为（能判断/能指出/能设计……），一句话。
- prerequisites 只能引用合并后列表中排在它前面的能力名，最多 3 个；没有就给空数组。
- evidence 保留草稿原文 quote 及来源类型：转录用 {"session":N}，上传资料用 {"source":N}，二选一；session/source 编号保持不变，不得互换；每项最多 2 条。`)
	if skeleton := skillMapSkeleton(previous); skeleton != "" {
		builder.WriteString(skillMapRebuildInstruction)
		builder.WriteString(skeleton)
	}
	return builder.String()
}

// skillMapTranscriptBudget is the transcript bytes one chunk call may carry.
// Coverage is mandatory (a course is never half-read), so the only limit is
// what the model window holds; fewer, larger chunks mean fewer calls. The
// project's chat context budget does not apply here.
func skillMapTranscriptBudget() int {
	budget := aicontext.MaxContextTokens()
	if budget > skillMapMaxChunkBytes {
		budget = skillMapMaxChunkBytes
	}
	if budget <= skillMapChunkInstructionOverhead+1024 {
		if budget < 1024 {
			return 1024
		}
		return budget / 2
	}
	return budget - skillMapChunkInstructionOverhead
}

func formatSkillMapMaterialBlock(material skillMapMaterial, body, continuation string) string {
	title := strings.TrimSpace(material.Title)
	var header string
	if material.Kind == "source" {
		if title == "" {
			title = "未命名资料"
		}
		header = fmt.Sprintf("[资料 %d] %s", material.Ordinal, title)
	} else {
		if title == "" {
			title = "未命名会话"
		}
		header = fmt.Sprintf("[会话 %d] %s（%s）", material.Ordinal, title, material.Date)
	}
	if continuation != "" {
		header += continuation
	}
	return header + "\n" + body
}

// skillMapMaterials lines up sessions (oldest first) then uploaded course
// materials, numbering each kind from 1 so the model can cite either.
func skillMapMaterials(
	sessions []store.ProjectSessionRef, transcripts []string, sources []skillMapSourceRef,
) []skillMapMaterial {
	materials := make([]skillMapMaterial, 0, len(sessions)+len(sources))
	for index, session := range sessions {
		body := ""
		if index < len(transcripts) {
			body = transcripts[index]
		}
		materials = append(materials, skillMapMaterial{
			Kind: "session", Ordinal: index + 1, Title: session.Title,
			Date: session.StartedAt.Format("2006-01-02"), Body: body,
		})
	}
	for index, source := range sources {
		materials = append(materials, skillMapMaterial{
			Kind: "source", Ordinal: index + 1, Title: source.Title, Body: source.Text,
		})
	}
	return materials
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

// packSkillMapChunks groups course materials into model-sized pieces without
// dropping any text. Materials that fit stay whole; an oversize one is split
// in order, each piece still labeled with the same ordinal.
func packSkillMapChunks(materials []skillMapMaterial, budget int) []string {
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
	for _, material := range materials {
		body := strings.TrimSpace(material.Body)
		if body == "" {
			continue
		}
		block := formatSkillMapMaterialBlock(material, body, "")
		if aicontext.EstimateTokens(block) <= budget {
			appendFit(block)
			continue
		}
		flush()
		headerBudget := aicontext.EstimateTokens(formatSkillMapMaterialBlock(material, "x", "（续）"))
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
			appendFit(formatSkillMapMaterialBlock(material, piece, continuation))
			flush()
		}
	}
	flush()
	return chunks
}
