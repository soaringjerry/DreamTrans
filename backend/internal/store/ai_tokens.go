package store

import aicontext "github.com/dreamtrans/backend/internal/ai"

// conservativeTokenCount keeps persisted/provider-batch estimates aligned with
// ai.EstimateTokens. A caller may supply a larger model-specific estimate, but
// never a smaller one that could understate CJK or unusual UTF-8 input.
func conservativeTokenCount(content string, supplied int) int {
	estimated := aicontext.EstimateTokens(content)
	if supplied < estimated {
		return estimated
	}
	return supplied
}
