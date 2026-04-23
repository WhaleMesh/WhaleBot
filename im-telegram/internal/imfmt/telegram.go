package imfmt

import (
	"html"
	"regexp"
	"strings"
)

var (
	reCodeFence = regexp.MustCompile("(?s)```(?:[a-zA-Z0-9_+-]+)?\\n(.*?)```")
	reInline    = regexp.MustCompile("`([^`]+)`")
	reLink      = regexp.MustCompile(`\[(.+?)\]\((https?://[^\s)]+)\)`)
	reBold1     = regexp.MustCompile(`\*\*(.+?)\*\*`)
	reBold2     = regexp.MustCompile(`__(.+?)__`)
	reItalic1   = regexp.MustCompile(`\*(.+?)\*`)
	reItalic2   = regexp.MustCompile(`_(.+?)_`)
)

// MarkdownToTelegramHTML converts standard Markdown text into a Telegram-friendly
// HTML subset. This keeps IM output readable while preserving common formatting.
func MarkdownToTelegramHTML(md string) string {
	if strings.TrimSpace(md) == "" {
		return ""
	}

	type block struct {
		token string
		html  string
	}
	var blocks []block

	// Protect fenced code blocks before applying inline transforms.
	working := reCodeFence.ReplaceAllStringFunc(md, func(m string) string {
		sub := reCodeFence.FindStringSubmatch(m)
		code := ""
		if len(sub) > 1 {
			code = sub[1]
		}
		token := "__TG_CODE_BLOCK_" + strings.Repeat("X", len(blocks)+1) + "__"
		blocks = append(blocks, block{
			token: token,
			html:  "<pre><code>" + html.EscapeString(strings.TrimSuffix(code, "\n")) + "</code></pre>",
		})
		return token
	})

	working = html.EscapeString(working)

	// Headings -> bold line.
	lines := strings.Split(working, "\n")
	for i := range lines {
		line := strings.TrimSpace(lines[i])
		if strings.HasPrefix(line, "#") {
			title := strings.TrimSpace(strings.TrimLeft(line, "#"))
			lines[i] = "<b>" + title + "</b>"
			continue
		}
		if strings.HasPrefix(line, "- ") || strings.HasPrefix(line, "* ") {
			lines[i] = "• " + strings.TrimSpace(line[2:])
		}
	}
	working = strings.Join(lines, "\n")

	working = reLink.ReplaceAllString(working, `<a href="$2">$1</a>`)
	working = reBold1.ReplaceAllString(working, `<b>$1</b>`)
	working = reBold2.ReplaceAllString(working, `<b>$1</b>`)
	working = reInline.ReplaceAllString(working, `<code>$1</code>`)
	working = reItalic1.ReplaceAllString(working, `<i>$1</i>`)
	working = reItalic2.ReplaceAllString(working, `<i>$1</i>`)

	for _, b := range blocks {
		working = strings.ReplaceAll(working, html.EscapeString(b.token), b.html)
	}
	return working
}
