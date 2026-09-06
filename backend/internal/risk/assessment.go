package risk

import (
	"context"
	"database/sql"
)

// Evidence describes observed correlations, not proof that two people are one.
type Evidence struct {
	NetworkBurst     int    `json:"network_burst"`
	PrefixHourly     int    `json:"prefix_hourly"`
	FingerprintCount int    `json:"fingerprint_count"`
	LinkedDenied     int    `json:"linked_denied"`
	Browser          string `json:"browser"`
	Platform         string `json:"platform"`
}

func enrichAssessment(ctx context.Context, tx *sql.Tx, s *Signals, a *Assessment) error {
	e := &a.Evidence
	e.Browser, e.Platform = s.Browser, s.Platform
	err := tx.QueryRowContext(ctx, `SELECT
 (SELECT COUNT(*) FROM signup_risk_profiles WHERE network_hash=$1 AND created_at>NOW()-INTERVAL '10 minutes'),
 (SELECT COUNT(*) FROM signup_risk_profiles WHERE prefix_hash=$2 AND created_at>NOW()-INTERVAL '1 hour'),
 (SELECT COUNT(*) FROM signup_risk_profiles WHERE fingerprint_hash=$3 AND created_at>NOW()-INTERVAL '30 days'),
 (SELECT COUNT(*) FROM signup_risk_profiles WHERE decision='denied' AND created_at>NOW()-INTERVAL '30 days' AND
 (device_hash=$4 OR (fingerprint_hash=$3 AND prefix_hash=$2)))`, s.NetworkHash, s.PrefixHash, s.FingerprintHash, s.DeviceHash).Scan(&e.NetworkBurst, &e.PrefixHourly, &e.FingerprintCount, &e.LinkedDenied)
	if err != nil {
		return err
	}
	if e.NetworkBurst >= a.Rules.NetworkBurstLimit {
		a.Reasons = append(a.Reasons, "network_burst")
	}
	if e.PrefixHourly >= a.Rules.PrefixHourlyLimit {
		a.Reasons = append(a.Reasons, "prefix_velocity")
	}
	// A common device configuration alone is only evidence, not a trigger.
	if e.FingerprintCount >= 2 && (e.PrefixHourly >= 2 || e.NetworkBurst >= 1) {
		a.Reasons = append(a.Reasons, "fingerprint_cluster")
	}
	if e.LinkedDenied > 0 {
		a.Reasons = append(a.Reasons, "linked_denied")
	}
	a.Reasons = append(a.Reasons, s.BrowserReasons...)
	weights := map[string]int{"previous_email": 60, "missing_device": 25, "missing_network": 25, "device_accounts": 50, "network_velocity": 35, "daily_cap": 0, "network_burst": 35, "prefix_velocity": 30, "fingerprint_cluster": 40, "linked_denied": 60, "automation": 50, "ua_missing": 15, "browser_missing": 15, "browser_inconsistent": 25}
	for _, reason := range a.Reasons {
		a.Score += weights[reason]
	}
	if a.Score > 100 {
		a.Score = 100
	}
	return nil
}
