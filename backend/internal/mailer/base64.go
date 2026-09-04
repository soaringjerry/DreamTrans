package mailer

import "encoding/base64"

func base64Std(value string) string {
	return base64.StdEncoding.EncodeToString([]byte(value))
}
