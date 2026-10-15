package provider

import (
	"crypto/sha1" // the AWS CLI's cache file names; not for security
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

// SSOToken is the IAM Identity Center access token the AWS CLI cached for a
// portal (in ~/.aws/sso/cache, where aws sso login and mek login put it);
// "" when there is none or it is about to expire. mek reads it only to list
// the portal's accounts and roles, and never keeps it.
func SSOToken(startURL string) string {
	home, _ := os.UserHomeDir()
	// The cache file is named after the sso-session (mek's profiles use one),
	// or, for older logins without one, after the portal's URL.
	for _, key := range []string{sessionName(startURL), startURL} {
		sum := sha1.Sum([]byte(key))
		b, err := os.ReadFile(filepath.Join(home, ".aws", "sso", "cache", hex.EncodeToString(sum[:])+".json"))
		if err != nil {
			continue
		}
		var t struct{ AccessToken, ExpiresAt string }
		json.Unmarshal(b, &t) // unreadable: as if there were none
		if t.AccessToken != "" && time.Until(expiry(t.ExpiresAt)) > time.Minute {
			return t.AccessToken
		}
	}
	return ""
}

// expiry reads a cached token's expiresAt; older AWS CLIs wrote "…UTC".
// Unreadable is expired.
func expiry(s string) time.Time {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t
	}
	t, _ := time.Parse("2006-01-02T15:04:05UTC", s)
	return t
}

// WithTokenFile hands an access token to the AWS CLI where nobody else can
// see it: in a file only you can read, for --cli-input-json — not in argv,
// which ps shows to every user. The file is in dir, and is removed when f
// returns, whatever f did.
func WithTokenFile(dir, token string, f func(path string) error) error {
	os.MkdirAll(dir, 0o700)                     // a failure shows in CreateTemp's error
	tf, err := os.CreateTemp(dir, "sso-*.json") // 0600
	if err != nil {
		return err
	}
	defer os.Remove(tf.Name())
	b, _ := json.Marshal(map[string]string{"accessToken": token}) // a map of strings always marshals
	_, err = tf.Write(b)
	if err = errors.Join(err, tf.Close()); err == nil {
		err = f(tf.Name())
	}
	return err
}
