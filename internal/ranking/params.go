package ranking

import (
	"errors"
	"strconv"
	"strings"
)

func Type(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "hot" {
		value = "playback"
	}
	switch value {
	case "ranking", "playback", "top250", "popular":
		return value, nil
	}
	return "", errors.New("榜单类型必须是 ranking、playback、top250 或 popular")
}

func Period(kind, value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if kind == "top250" {
		if value == "" || value == "all" || value == "daily" {
			return "all", nil
		}
		if len(value) == 4 {
			if year, err := strconv.Atoi(value); err == nil && year >= 1900 && year <= 2100 {
				return value, nil
			}
		}
		return "", errors.New("TOP 250 年份必须是 all 或 1900 到 2100 的四位年份")
	}
	if value == "" {
		value = "daily"
	}
	if value == "daily" || value == "weekly" || value == "monthly" {
		return value, nil
	}
	return "", errors.New("榜单周期必须是 daily、weekly 或 monthly")
}
