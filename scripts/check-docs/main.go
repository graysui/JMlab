// check-docs checks maintained documentation and local OpenAPI structure.
// It does not contact external links or claim complete OpenAPI validation.
package main

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

func main() {
	root, err := filepath.Abs("../..")
	if err == nil {
		err = check(root)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func check(root string) error {
	readme, err := os.ReadFile(filepath.Join(root, "README.md"))
	if err != nil {
		return err
	}
	linkPattern := regexp.MustCompile(`\]\(([^)]+)\)`)
	files := []string{"README.md"}
	seen := map[string]bool{"README.md": true}
	for _, match := range linkPattern.FindAllStringSubmatch(string(readme), -1) {
		target := strings.TrimSpace(match[1])
		if strings.HasPrefix(target, "<") {
			if end := strings.Index(target, ">"); end >= 0 {
				target = target[1:end]
			}
		} else if fields := strings.Fields(target); len(fields) > 0 {
			target = fields[0]
		}
		if strings.HasPrefix(target, "#") || strings.Contains(target, ":") || strings.HasPrefix(target, "//") {
			continue
		}
		target, _, _ = strings.Cut(target, "#")
		target, err = url.PathUnescape(target)
		if err != nil {
			return fmt.Errorf("README.md: 链接转义无效: %w", err)
		}
		target = filepath.Clean(filepath.FromSlash(target))
		if strings.EqualFold(filepath.Ext(target), ".md") && !seen[target] {
			files = append(files, target)
			seen[target] = true
		}
	}
	links := 0
	for _, file := range files {
		path := filepath.Join(root, file)
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, match := range linkPattern.FindAllStringSubmatch(string(data), -1) {
			target := strings.TrimSpace(match[1])
			if strings.HasPrefix(target, "<") {
				end := strings.Index(target, ">")
				if end < 0 {
					return fmt.Errorf("%s: 无效链接 %s", file, target)
				}
				target = target[1:end]
			} else if fields := strings.Fields(target); len(fields) > 0 {
				target = fields[0]
			}
			if target == "" || strings.HasPrefix(target, "#") || strings.Contains(target, ":") || strings.HasPrefix(target, "//") {
				continue
			}
			target, _, _ = strings.Cut(target, "#")
			target, err = url.PathUnescape(target)
			if err != nil {
				return fmt.Errorf("%s: 链接转义无效: %w", file, err)
			}
			destination := filepath.Join(filepath.Dir(path), filepath.FromSlash(target))
			rel, err := filepath.Rel(root, destination)
			if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				return fmt.Errorf("%s: 链接超出仓库: %s", file, target)
			}
			if _, err := os.Stat(destination); err != nil {
				return fmt.Errorf("%s: 本地链接不存在 %s: %w", file, target, err)
			}
			links++
		}
	}
	data, err := os.ReadFile(filepath.Join(root, "docs", "openapi.yaml"))
	if err != nil {
		return err
	}
	var doc map[string]any
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return fmt.Errorf("OpenAPI YAML: %w", err)
	}
	if doc["openapi"] != "3.1.0" {
		return fmt.Errorf("OpenAPI 版本应为 3.1.0")
	}
	refs, patterns := 0, 0
	var walk func(any) error
	walk = func(value any) error {
		switch node := value.(type) {
		case map[string]any:
			for key, child := range node {
				if key == "$ref" {
					ref, ok := child.(string)
					if !ok || !strings.HasPrefix(ref, "#/") {
						return fmt.Errorf("只检查本地 OpenAPI 引用: %v", child)
					}
					var target any = doc
					for _, part := range strings.Split(ref[2:], "/") {
						part = strings.ReplaceAll(strings.ReplaceAll(part, "~1", "/"), "~0", "~")
						object, ok := target.(map[string]any)
						if !ok {
							return fmt.Errorf("OpenAPI 引用无效: %s", ref)
						}
						target, ok = object[part]
						if !ok {
							return fmt.Errorf("OpenAPI 引用不存在: %s", ref)
						}
					}
					refs++
				}
				if key == "pattern" {
					pattern, ok := child.(string)
					if !ok {
						return fmt.Errorf("OpenAPI pattern 必须为字符串")
					}
					if _, err := regexp.Compile(pattern); err != nil {
						return fmt.Errorf("OpenAPI pattern: %w", err)
					}
					patterns++
				}
				if err := walk(child); err != nil {
					return err
				}
			}
		case []any:
			for _, child := range node {
				if err := walk(child); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := walk(doc); err != nil {
		return err
	}
	fmt.Printf("通过：%d 份 Markdown、%d 个本地文件链接；OpenAPI YAML、%d 个内部引用、%d 个正则。\n", len(files), links, refs, patterns)
	return nil
}
