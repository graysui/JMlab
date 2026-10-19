// Package fixture used only by isolated browser tests, never by production startup.
package main

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"os"
)

func main() {
	http.HandleFunc("/api/v1/caps", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+os.Getenv("JMLAB_MODULE_TOKEN") {
			http.Error(w, "unauthorized", 401)
			return
		}
		data, _ := base64.StdEncoding.DecodeString(r.Header.Get("X-JMlab-Module-Config"))
		var config map[string]any
		_ = json.Unmarshal(data, &config)
		if config["apiKey"] != "browser-package-secret" {
			http.Error(w, "invalid config", 401)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"supportsSearch": false, "supportedTypes": []string{"movie"}})
	})
	if err := http.ListenAndServe(os.Getenv("JMLAB_MODULE_LISTEN_ADDR"), nil); err != nil {
		os.Exit(1)
	}
}
