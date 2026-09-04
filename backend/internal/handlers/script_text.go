package handlers

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// cjkRuneWeight is the reading weight of one Han/kana/fullwidth rune measured
// in Latin characters. Every chunking threshold in this package was tuned on
// English; weighting CJK runes keeps those thresholds meaningful for Chinese
// and Japanese without maintaining a second set of numbers.
const cjkRuneWeight = 3

// isSpacelessCJK reports whether r belongs to a script written without spaces
// between words (Han, kana, bopomofo, CJK punctuation, fullwidth forms).
// Hangul is excluded: Korean separates words with spaces.
func isSpacelessCJK(r rune) bool {
	switch {
	case r >= 0x2E80 && r <= 0x303F,
		r >= 0x3040 && r <= 0x30FF,
		r >= 0x3100 && r <= 0x312F,
		r >= 0x31C0 && r <= 0x9FFF,
		r >= 0xF900 && r <= 0xFAFF,
		r >= 0xFF00 && r <= 0xFFEF:
		return true
	}
	return false
}

// isHangul reports whether r is a Hangul syllable or compatibility jamo.
func isHangul(r rune) bool {
	return (r >= 0xAC00 && r <= 0xD7AF) || (r >= 0x3130 && r <= 0x318F)
}

// textWeight measures text in Latin-equivalent characters: CJK runes count
// cjkRuneWeight, everything else counts one.
func textWeight(s string) int {
	weight := 0
	for _, r := range s {
		if isSpacelessCJK(r) {
			weight += cjkRuneWeight
		} else {
			weight++
		}
	}
	return weight
}

// leadingNoSpacePunctuation lists characters that never take a space before
// them when a fragment starts with one.
const leadingNoSpacePunctuation = ",.;:!?%)]}»”’…、，。；：！？』」）】"

// joinFragments concatenates two transcript fragments the way a reader of the
// script expects: "Hi." + "Hello." → "Hi. Hello.", "你好。" + "今天" → "你好。今天",
// and no space before a fragment that is only trailing punctuation.
func joinFragments(left, right string) string {
	head := strings.TrimRightFunc(left, unicode.IsSpace)
	tail := strings.TrimSpace(right)
	if head == "" {
		return tail
	}
	if tail == "" {
		return head
	}
	last, _ := utf8.DecodeLastRuneInString(head)
	first, _ := utf8.DecodeRuneInString(tail)
	if strings.ContainsRune(leadingNoSpacePunctuation, first) ||
		isSpacelessCJK(last) || isHangul(last) ||
		isSpacelessCJK(first) || isHangul(first) {
		return head + tail
	}
	return head + " " + tail
}
