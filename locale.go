package main

import (
	"embed"
	"encoding/json"
	"fmt"
)

//go:embed locales/*.json
var localeFiles embed.FS

type Text struct {
	Offline     string `json:"offline"`
	Online      string `json:"online"`
	Updated     string `json:"updated"`
	Started     string `json:"started"`
	Live        string `json:"live"`
	Stats       string `json:"stats"`
	Unavailable string `json:"unavailable"`
	Stale       string `json:"stale"`
	Never       string `json:"never"`
	Failure     string `json:"failure"`
	Recovered   string `json:"recovered"`
	More        string `json:"more"`
	Checking    string `json:"checking"`
}

func loadText(language string) (Text, error) {
	var t Text
	data, err := localeFiles.ReadFile("locales/" + language + ".json")
	if err != nil {
		return t, err
	}
	if err = json.Unmarshal(data, &t); err != nil {
		return t, fmt.Errorf("invalid language file: %w", err)
	}
	return t, nil
}
