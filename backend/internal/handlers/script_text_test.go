package handlers

import "testing"

func TestJoinFragmentsIsScriptAware(t *testing.T) {
	cases := []struct{ left, right, want string }{
		{"Hi.", "Hello.", "Hi. Hello."},
		{"Hello", ", world", "Hello, world"},
		{"你好。", "今天天气", "你好。今天天气"},
		{"今日は", "。", "今日は。"},
		{"안녕하세요", "반갑습니다", "안녕하세요반갑습니다"},
		{"the word", "中文", "the word中文"},
		{"", "tail", "tail"},
		{"head  ", "", "head"},
		{"He said", "\"stop\".", "He said \"stop\"."},
	}
	for _, tc := range cases {
		if got := joinFragments(tc.left, tc.right); got != tc.want {
			t.Fatalf("joinFragments(%q, %q) = %q, want %q", tc.left, tc.right, got, tc.want)
		}
	}
}

func TestTextWeightCountsCJKRunesHeavier(t *testing.T) {
	if got := textWeight("hello world"); got != 11 {
		t.Fatalf("latin weight = %d, want 11", got)
	}
	if got := textWeight("你好"); got != 2*cjkRuneWeight {
		t.Fatalf("han weight = %d, want %d", got, 2*cjkRuneWeight)
	}
	if got := textWeight("안녕"); got != 2 {
		t.Fatalf("hangul weight = %d, want 2 (Korean uses spaces, counts like Latin)", got)
	}
}

func TestIsSentenceEndingToleratesClosingQuotesAndBrackets(t *testing.T) {
	for _, s := range []string{"Done.", "Really?", "他说：“走吧。”", "「終わりました。」", "He said \"stop.\"", "(fin.)", "好的！"} {
		if !isSentenceEnding(s) {
			t.Fatalf("%q should end a sentence", s)
		}
	}
	for _, s := range []string{"Done", "他说，", "\"\"", "", "今天"} {
		if isSentenceEnding(s) {
			t.Fatalf("%q should not end a sentence", s)
		}
	}
}

func TestHandleAggregationDoesNotInsertSpacesInsideCJK(t *testing.T) {
	state := defaultConnState()
	state.flushGapSeconds = 10
	if flushed, _, _, _ := state.handleAggregation("S1", "我们今天", 0, 1); flushed {
		t.Fatal("unfinished CJK fragment should buffer")
	}
	flushed, text, _, _ := state.handleAggregation("S1", "讨论一下。", 1, 2)
	if !flushed {
		t.Fatal("sentence-final fragment should flush")
	}
	if text != "我们今天讨论一下。" {
		t.Fatalf("aggregated CJK text = %q", text)
	}

	if flushed, _, _, _ := state.handleAggregation("S2", "Let us", 0, 1); flushed {
		t.Fatal("unfinished Latin fragment should buffer")
	}
	flushed, text, _, _ = state.handleAggregation("S2", "begin.", 1, 2)
	if !flushed || text != "Let us begin." {
		t.Fatalf("aggregated Latin text = %q (flushed=%v)", text, flushed)
	}
}

func TestCombineSentencesIsScriptAware(t *testing.T) {
	got, _, _ := combineSentences([]sentence{{text: "第一句。"}, {text: "第二句。"}})
	if got != "第一句。第二句。" {
		t.Fatalf("CJK paragraph = %q", got)
	}
	got, _, _ = combineSentences([]sentence{{text: "First."}, {text: "Second."}})
	if got != "First. Second." {
		t.Fatalf("Latin paragraph = %q", got)
	}
}

func TestFilterLowInfoTextKeepsShortCJKSentences(t *testing.T) {
	if got := filterLowInfoText("今天讨论定价策略。"); got == "" {
		t.Fatal("a nine-character Chinese sentence carries as much as a long English one and must survive")
	}
	if got := filterLowInfoText("ok. um. yes."); got != "" {
		t.Fatalf("English filler should still be dropped, got %q", got)
	}
}
