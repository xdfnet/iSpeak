package main

import (
	"regexp"
	"strings"
)

var (
	markdownLinkRe     = regexp.MustCompile(`\[[^\]]+\]\(([^)]*)\)`)
	absolutePathRe     = regexp.MustCompile(`/(?:Users|private|tmp|var|opt|usr|bin|sbin|etc|Library|Applications)/\S+`)
	commitHashRe       = regexp.MustCompile(`\b[0-9a-f]{7,40}\b`)
	uuidRe             = regexp.MustCompile(`\b[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}\b`)
	urlRe              = regexp.MustCompile(`https?://\S+`)
	ansiEscapeRe       = regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]`)
	multiSpaceRe       = regexp.MustCompile(`\s+`)
	markdownListRe     = regexp.MustCompile(`^\s*(?:[-*+]\s+|\d+[.)]\s+)`)
	htmlTagRe          = regexp.MustCompile(`<[^>]+>`)
	codeFenceStartRe   = regexp.MustCompile("^```")
	artifactStartRe    = regexp.MustCompile(`(?i)^<artifact\b`)
	htmlDocumentLineRe = regexp.MustCompile(`(?i)^<!doctype html|^<html\b|^<head\b|^<body\b|^<style\b|^</`)
	speedNoiseRe       = regexp.MustCompile(`(?i)\d+(?:\.\d+)?\s*(?:kb|mb|gb)/s`)
	etaNoiseRe         = regexp.MustCompile(`(?i)\bETA\b|预计剩余|剩余时间`)
)

// 过滤格式符号，保留自然朗读文本。
// 顺序很重要：先跳过跨行块结构，再跳过整行噪声，最后清理行内符号。
func cleanText(text string) string {
	var lines []string
	rawLines := strings.Split(text, "\n")
	inCodeBlock := false
	inArtifact := false
	inMarkdownTable := false
	for i := 0; i < len(rawLines); i++ {
		line := rawLines[i]
		line = strings.TrimSpace(line)
		if line == "" {
			inMarkdownTable = false
			continue
		}
		if codeFenceStartRe.MatchString(line) {
			inCodeBlock = !inCodeBlock
			continue
		}
		if inCodeBlock {
			continue
		}
		if artifactStartRe.MatchString(line) {
			inArtifact = !strings.Contains(strings.ToLower(line), "</artifact>")
			continue
		}
		if inArtifact {
			if strings.Contains(strings.ToLower(line), "</artifact>") {
				inArtifact = false
			}
			continue
		}
		if isMarkdownTableSeparator(line) {
			if len(lines) > 0 && isMarkdownTableRow(strings.TrimSpace(rawLines[i-1])) {
				lines = lines[:len(lines)-1]
			}
			inMarkdownTable = true
			continue
		}
		if inMarkdownTable {
			if isMarkdownTableRow(line) {
				continue
			}
			inMarkdownTable = false
		}
		if shouldSkipSpeechLine(line) {
			continue
		}

		cleaned := cleanSpeechLine(line)
		if cleaned != "" {
			lines = append(lines, cleaned)
		}
	}
	return strings.Join(lines, "，")
}

func shouldSkipSpeechLine(line string) bool {
	if isMarkdownTableSeparator(line) {
		return true
	}
	if strings.HasPrefix(line, "---") && strings.Count(line, "-") > 3 {
		return true
	}
	if htmlDocumentLineRe.MatchString(line) {
		return true
	}
	if isProgressNoiseLine(line) {
		return true
	}
	if isMostlyTableRow(line) {
		return true
	}
	return false
}

func isMarkdownTableSeparator(line string) bool {
	line = strings.TrimSpace(line)
	return strings.Contains(line, "|") && strings.Trim(line, "|-: ") == ""
}

func isMarkdownTableRow(line string) bool {
	line = strings.TrimSpace(line)
	return strings.Count(line, "|") >= 2
}

func cleanSpeechLine(line string) string {
	// Markdown 链接必须在 URL 删除前处理，否则会丢掉链接标题。
	line = ansiEscapeRe.ReplaceAllString(line, "")
	line = markdownListRe.ReplaceAllString(line, "")
	line = markdownLinkRe.ReplaceAllStringFunc(line, func(match string) string {
		if end := strings.Index(match, "]"); end > 1 {
			return match[1:end]
		}
		return ""
	})
	line = urlRe.ReplaceAllString(line, "")
	line = absolutePathRe.ReplaceAllString(line, " 路径 ")
	// UUID 必须在短 hash 前处理，避免先删短片段后破坏 UUID 识别。
	line = uuidRe.ReplaceAllString(line, "")
	line = commitHashRe.ReplaceAllString(line, "")
	line = htmlTagRe.ReplaceAllString(line, "")
	line = strings.NewReplacer(
		"**", "",
		"*", "",
		"`", "",
		"#", "",
		">", "",
		"✅", "",
		"❌", "",
		"✓", "",
		"✗", "",
		"→", "到",
	).Replace(line)
	line = strings.Trim(line, " \t-:|")
	line = multiSpaceRe.ReplaceAllString(line, " ")
	return strings.TrimSpace(line)
}

func isMostlyTableRow(line string) bool {
	if !strings.Contains(line, "|") {
		return false
	}
	return strings.Count(line, "|") >= 2 && len([]rune(line)) > 40
}

func isProgressNoiseLine(line string) bool {
	return speedNoiseRe.MatchString(line) || etaNoiseRe.MatchString(line)
}
