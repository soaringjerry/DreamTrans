package handlers

import (
	"encoding/csv"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/dreamtrans/backend/internal/billing"
)

// statementEpoch is the "everything" lower bound. The product did not exist
// before it, so it is cheaper than looking up when the account opened.
var statementEpoch = time.Date(2000, time.January, 1, 0, 0, 0, 0, time.UTC)

// parseStatementWindow reads the half-open window a statement request asks for.
// month=YYYY-MM selects one calendar month; from/to take YYYY-MM-DD, with `to`
// inclusive of the day named. With no parameters the window is the current
// month, which is what the account panel opens on.
func parseStatementWindow(query map[string][]string, now time.Time) (time.Time, time.Time, error) {
	first := func(key string) string {
		values := query[key]
		if len(values) == 0 {
			return ""
		}
		return strings.TrimSpace(values[0])
	}
	if month := first("month"); month != "" {
		start, err := time.Parse("2006-01", month)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("month must use YYYY-MM")
		}
		return start.UTC(), start.UTC().AddDate(0, 1, 0), nil
	}
	fromRaw, toRaw := first("from"), first("to")
	if fromRaw == "" && toRaw == "" {
		start := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
		return start, start.AddDate(0, 1, 0), nil
	}
	from := statementEpoch
	if fromRaw != "" {
		parsed, err := time.Parse("2006-01-02", fromRaw)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("from must use YYYY-MM-DD")
		}
		from = parsed.UTC()
	}
	to := now.UTC().AddDate(0, 0, 1).Truncate(24 * time.Hour)
	if toRaw != "" {
		parsed, err := time.Parse("2006-01-02", toRaw)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("to must use YYYY-MM-DD")
		}
		to = parsed.UTC().AddDate(0, 0, 1)
	}
	if !to.After(from) {
		return time.Time{}, time.Time{}, fmt.Errorf("to must be after from")
	}
	return from, to, nil
}

// HandleStatement returns one account's own billing records for a period, as
// JSON for the account panel or as CSV for the user to keep. The three record
// types are kept in one table so a spreadsheet can sort them by date; amounts
// are signed, so usage is negative and money paid in is positive.
func (h *BillingHandler) HandleStatement(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	claims, ok := requireActor(w, r)
	if !ok {
		return
	}
	from, to, err := parseStatementWindow(r.URL.Query(), timeNow())
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusBadRequest)
		return
	}
	statement, err := h.billing.UserStatement(r.Context(), claims.UserID, from, to)
	if err != nil {
		if errors.Is(err, billing.ErrInvalidBillingInput) {
			http.Error(w, `{"error":"invalid statement range"}`, http.StatusBadRequest)
			return
		}
		http.Error(w, `{"error":"failed to load statement"}`, http.StatusInternalServerError)
		return
	}
	if !strings.EqualFold(r.URL.Query().Get("format"), "csv") {
		WriteJSON(w, statement)
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	setDownloadFilename(w, statementFilename(from, to), "csv")
	if err := writeStatementCSV(w, statement); err != nil {
		// Headers are already out; the truncated body is the only signal left.
		return
	}
}

// statementFilename names the download after the window it covers, so a folder
// of exports stays sortable.
func statementFilename(from, to time.Time) string {
	last := to.AddDate(0, 0, -1)
	if from.Equal(time.Date(from.Year(), from.Month(), 1, 0, 0, 0, 0, time.UTC)) &&
		to.Equal(from.AddDate(0, 1, 0)) {
		return "yufolo-statement-" + from.Format("2006-01")
	}
	return "yufolo-statement-" + from.Format("2006-01-02") + "_" + last.Format("2006-01-02")
}

func csvUSD(amount float64) string { return strconv.FormatFloat(amount, 'f', 6, 64) }

func writeStatementCSV(w http.ResponseWriter, statement *billing.UserStatement) error {
	out := csv.NewWriter(w)
	if err := out.Write([]string{
		"record", "timestamp", "type", "description", "session_id", "model",
		"quantity", "input_tokens", "output_tokens", "amount_usd",
		"balance_after_usd", "paid_from", "status",
	}); err != nil {
		return err
	}
	for i := range statement.Usage {
		item := &statement.Usage[i]
		sessionID := ""
		if item.SessionID != nil {
			sessionID = *item.SessionID
		}
		paidFrom := "wallet"
		switch {
		case item.GrantUSD > 0 && item.WalletUSD > 0:
			paidFrom = "grant+wallet"
		case item.GrantUSD > 0:
			paidFrom = "grant"
		case item.Attribution == "byok":
			paidFrom = "byok"
		}
		status := "settled"
		switch {
		case item.Refunded:
			status = "refunded"
		case !item.Settled:
			status = "reserved"
		}
		if err := out.Write([]string{
			"usage", item.CreatedAt, item.Action, item.Feature, sessionID, item.Model,
			strconv.FormatFloat(item.Quantity, 'f', -1, 64),
			strconv.Itoa(item.InputTokens), strconv.Itoa(item.OutputTokens),
			csvUSD(-item.CostUSD), "", paidFrom, status,
		}); err != nil {
			return err
		}
	}
	for i := range statement.Payments {
		payment := &statement.Payments[i]
		if err := out.Write([]string{
			"payment", payment.CreatedAt, payment.Kind, payment.Description, "", "",
			"", "", "", csvUSD(payment.AmountUSD), "", "stripe", payment.Status,
		}); err != nil {
			return err
		}
	}
	for i := range statement.Ledger {
		entry := &statement.Ledger[i]
		reference := ""
		if entry.ReferenceType != nil {
			reference = *entry.ReferenceType
		}
		if err := out.Write([]string{
			"balance", entry.CreatedAt, entry.TransactionType, entry.Description, "", "",
			"", "", "", csvUSD(entry.AmountUSD), csvUSD(entry.BalanceAfterUSD),
			entry.Bucket, reference,
		}); err != nil {
			return err
		}
	}
	out.Flush()
	return out.Error()
}
