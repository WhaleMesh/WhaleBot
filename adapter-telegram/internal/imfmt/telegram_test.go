package imfmt

import (
	"strings"
	"testing"
)

func TestMarkdownToTelegramHTML_ReplacesFencedCodeBlocks(t *testing.T) {
	input := "Go 程序\n```go\nfmt.Println(\"hi\")\n```\n运行方式"
	out := MarkdownToTelegramHTML(input)

	if strings.Contains(out, "TGCODEBLOCKTOKEN") {
		t.Fatalf("placeholder leaked in output: %s", out)
	}
	if !strings.Contains(out, "<pre><code>fmt.Println(&#34;hi&#34;)</code></pre>") {
		t.Fatalf("expected fenced code rendered as pre/code, got: %s", out)
	}
}
