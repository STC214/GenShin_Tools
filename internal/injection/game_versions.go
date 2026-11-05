package injection

import (
	"fmt"
	"strconv"
	"strings"
)

// CheckGameVersionRule implements the public Fufu General.GameVersion grammar.
// It is additional to, never a replacement for, the exact locally audited
// GameVersions list. An absent declaration preserves legacy package behavior.
func CheckGameVersionRule(rule, gameVersion string) error {
	if strings.TrimSpace(rule) == "" {
		return nil
	}
	if len(rule) > 4096 {
		return fmt.Errorf("plugin game-version rule exceeds 4096 bytes")
	}
	version, err := parseRuleVersion(gameVersion)
	if err != nil {
		return fmt.Errorf("plugin game-version check: %w", err)
	}
	normalized := strings.ToLower(strings.TrimSpace(rule))
	normalized = strings.NewReplacer("或者", ",", "或", ",", "并且", "&", "且", "&",
		"排除", ",排除", "除了", ",除了", "不含", ",不含", "不支持", ",不支持",
		" except ", ",except ", " exclude ", ",exclude ", " not ", ",not ", "!", ",!", "！", ",!").Replace(normalized)
	groups := strings.FieldsFunc(normalized, func(r rune) bool { return strings.ContainsRune(",，、;；|\n\r\t", r) })
	matched, excluded, clauses, positives := false, false, 0, 0
	for _, group := range groups {
		all, count := true, 0
		for _, raw := range strings.FieldsFunc(group, func(r rune) bool { return r == '&' || r == '＆' }) {
			segment := strings.TrimSpace(raw)
			negative := false
			for _, prefix := range []string{"!", "exclude", "except", "not", "不支持", "排除", "除了", "不含"} {
				if strings.HasPrefix(segment, prefix) {
					negative, segment = true, strings.TrimSpace(strings.TrimPrefix(segment, prefix))
					break
				}
			}
			ok, err := matchVersionCondition(segment, version)
			if err != nil {
				return fmt.Errorf("malformed plugin game-version rule %q: %w", rule, err)
			}
			clauses++
			if clauses > 128 {
				return fmt.Errorf("plugin game-version rule exceeds 128 clauses")
			}
			if negative {
				excluded = excluded || ok
			} else {
				all, count, positives = all && ok, count+1, positives+1
			}
		}
		matched = matched || count > 0 && all
	}
	if clauses == 0 {
		return fmt.Errorf("malformed plugin game-version rule %q", rule)
	}
	if excluded || positives > 0 && !matched {
		return fmt.Errorf("game version %q is not supported by plugin rule %q", gameVersion, rule)
	}
	return nil
}

func parseRuleVersion(value string) ([]int, error) {
	value = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(value)), "v")
	parts := strings.Split(value, ".")
	if len(parts) > 4 || len(parts) == 0 {
		return nil, fmt.Errorf("invalid game version %q", value)
	}
	var result []int
	wildcard := false
	for _, part := range parts {
		if part == "*" || part == "x" {
			wildcard = true
			continue
		}
		if wildcard || part == "" || len(part) > 4 || strings.IndexFunc(part, func(r rune) bool { return r < '0' || r > '9' }) >= 0 {
			return nil, fmt.Errorf("invalid game version %q", value)
		}
		number, err := strconv.Atoi(part)
		if err != nil {
			return nil, fmt.Errorf("invalid game version %q", value)
		}
		result = append(result, number)
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("invalid game version %q", value)
	}
	return result, nil
}

func compareRuleVersions(a, b []int) int {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] < b[i] {
			return -1
		}
		if a[i] > b[i] {
			return 1
		}
	}
	return 0
}

func matchVersionCondition(value string, version []int) (bool, error) {
	for _, any := range []string{"*", "any", "all", "anything", "全部", "所有", "任意", "全版本", "通用"} {
		if value == any {
			return true, nil
		}
	}
	for _, operator := range []string{">=", "=>", "≥", "<=", "=<", "≤", "==", ">", "<", "＞", "＜", "="} {
		if strings.HasPrefix(value, operator) {
			bound, err := parseRuleVersion(strings.TrimPrefix(value, operator))
			if err != nil {
				return false, err
			}
			c := compareRuleVersions(version, bound)
			switch operator {
			case ">=", "=>", "≥":
				return c >= 0, nil
			case "<=", "=<", "≤":
				return c <= 0, nil
			case ">", "＞":
				return c > 0, nil
			case "<", "＜":
				return c < 0, nil
			default:
				return c == 0, nil
			}
		}
	}
	if strings.HasSuffix(value, "+") {
		return matchVersionCondition(">="+strings.TrimSuffix(value, "+"), version)
	}
	for _, separator := range []string{"..", "~", "～", "-", "－", "–", "—", " to ", "到", "至"} {
		if left, right, ok := strings.Cut(value, separator); ok {
			var low, high []int
			var err error
			if strings.TrimSpace(left) != "" {
				low, err = parseRuleVersion(left)
				if err != nil {
					return false, err
				}
			}
			if strings.TrimSpace(right) != "" {
				high, err = parseRuleVersion(right)
				if err != nil {
					return false, err
				}
			}
			if len(low)+len(high) == 0 || len(low) > 0 && len(high) > 0 && compareRuleVersions(low, high) > 0 {
				return false, fmt.Errorf("invalid version range %q", value)
			}
			return (len(low) == 0 || compareRuleVersions(version, low) >= 0) && (len(high) == 0 || compareRuleVersions(version, high) <= 0), nil
		}
	}
	bound, err := parseRuleVersion(value)
	return err == nil && compareRuleVersions(version, bound) == 0, err
}
