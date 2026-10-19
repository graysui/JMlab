package repository

import (
	"strconv"
	"strings"
)

// Rebind converts the portable ? placeholder used by the application to the
// numbered placeholders required by PostgreSQL. SQLite keeps ? unchanged.
// Quoted SQL and comments are preserved. Bare ? is reserved for parameters;
// application queries do not use PostgreSQL's question-mark JSON operators.
func (d *Database) Rebind(query string) string {
	if d == nil || d.dialect != "postgres" {
		return query
	}
	var builder strings.Builder
	builder.Grow(len(query))
	argument := 1
	for index := 0; index < len(query); {
		end := index + 1
		switch query[index] {
		case '\'', '"':
			quote := query[index]
			escaped := quote == '\'' && index > 0 && (query[index-1] == 'e' || query[index-1] == 'E') && (index < 2 || !sqlIdentifierByte(query[index-2]))
			for end < len(query) {
				if escaped && query[end] == '\\' && end+1 < len(query) {
					end += 2
					continue
				}
				if query[end] == quote {
					end++
					if end < len(query) && query[end] == quote {
						end++
						continue
					}
					break
				}
				end++
			}
		case '-':
			if end < len(query) && query[end] == '-' {
				for end < len(query) && query[end] != '\n' && query[end] != '\r' {
					end++
				}
			}
		case '/':
			if end < len(query) && query[end] == '*' {
				end++
				for depth := 1; end < len(query) && depth > 0; end++ {
					if end+1 < len(query) && query[end:end+2] == "/*" {
						depth++
						end++
					} else if end+1 < len(query) && query[end:end+2] == "*/" {
						depth--
						end++
					}
				}
			}
		case '$':
			if index == 0 || !sqlIdentifierByte(query[index-1]) {
				tagEnd := end
				for tagEnd < len(query) && sqlIdentifierByte(query[tagEnd]) && query[tagEnd] != '$' {
					tagEnd++
				}
				if tagEnd < len(query) && query[tagEnd] == '$' && (tagEnd == end || query[end] < '0' || query[end] > '9') {
					tag := query[index : tagEnd+1]
					end = len(query)
					if closeAt := strings.Index(query[tagEnd+1:], tag); closeAt >= 0 {
						end = tagEnd + 1 + closeAt + len(tag)
					}
				}
			}
		case '?':
			builder.WriteString("$")
			builder.WriteString(strconv.Itoa(argument))
			argument++
			index++
			continue
		}
		builder.WriteString(query[index:end])
		index = end
	}
	return builder.String()
}

func sqlIdentifierByte(value byte) bool {
	return value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z' || value >= '0' && value <= '9' || value == '_' || value == '$' || value >= 0x80
}
