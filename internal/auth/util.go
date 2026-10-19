package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
)

func newID(prefix string) string {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		panic(fmt.Sprintf("生成 ID 失败: %v", err))
	}
	return prefix + "_" + hex.EncodeToString(bytes)
}

func randomToken() (string, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(bytes), nil
}

func csrfToken(masterKey, sessionToken string) string {
	mac := hmac.New(sha256.New, []byte(masterKey))
	_, _ = mac.Write([]byte(sessionToken))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func validCSRF(masterKey, sessionToken, provided string) bool {
	want := csrfToken(masterKey, sessionToken)
	return hmac.Equal([]byte(want), []byte(strings.TrimSpace(provided)))
}

func encodePreferences(preferences Preferences) (string, string, error) {
	if preferences.Theme == nil {
		preferences.Theme = map[string]any{}
	}
	if preferences.ModuleFlags == nil {
		preferences.ModuleFlags = map[string]any{}
	}
	theme, err := json.Marshal(preferences.Theme)
	if err != nil {
		return "", "", err
	}
	flags, err := json.Marshal(preferences.ModuleFlags)
	if err != nil {
		return "", "", err
	}
	return string(theme), string(flags), nil
}

func decodePreferences(theme, flags, locale string, adultValid bool, adultValue string) Preferences {
	preferences := Preferences{Theme: map[string]any{}, ModuleFlags: map[string]any{}, Locale: locale}
	if preferences.Locale == "" {
		preferences.Locale = "zh-CN"
	}
	_ = json.Unmarshal([]byte(theme), &preferences.Theme)
	_ = json.Unmarshal([]byte(flags), &preferences.ModuleFlags)
	if adultValid {
		value := adultValue
		preferences.AdultConfirmedAt = &value
	}
	return preferences
}
