package handlers

import (
    "net/http"
)

// default prompts should mirror backend provider logic to keep consistent resets
func defaultTranslatePrompt() string {
    return "您是一位专业的同声传译翻译，你正在把英文的口语内容翻译成中文易于理解的话，请使用 <context> 来帮助你理解上下文和当前场景并作出适当的纠错和润色。请仅翻译 <text>...</text> 里的文本变成中文，然后对中文进行润色，使其流畅、自然、易读，同时保留原文含义和语气。请尽量使用简洁、地道的措辞；根据需要合并不完整的句子；修改不合适的词序；删除填充词。请保持专业术语的准确性；保留数字/单位；并在适当的情况下将标点符号标准化为中文格式。请勿在输出中包含 <context> 中的任何内容。请勿添加解释、引述、说话者标签、时间戳或语言标签。仅返回最终润色后的中文句子，其他内容请勿返回。"
}

func defaultSummaryPrompt() string {
    return "You are a precise context compressor. Summarize English conversation text for downstream translation. Keep names, entities, topics, and unresolved references. Keep it concise and information-dense. Output in English."
}

func defaultChatPrompt() string {
    return "You are a helpful learning assistant. Answer in Chinese, structured and easy to skim. If context is insufficient, say you are unsure. Format rules: - Use short paragraphs and bullet points. - Start bullets with '- ' and put each on a new line. - Preserve line breaks for readability."
}

// HandlePromptDefaults returns backend default prompts for chat/translation/summary.
func HandlePromptDefaults(w http.ResponseWriter, r *http.Request) {
    writeJSON(w, map[string]any{
        "prompt_chat_default":      defaultChatPrompt(),
        "prompt_translate_default": defaultTranslatePrompt(),
        "prompt_summary_default":   defaultSummaryPrompt(),
    })
}

