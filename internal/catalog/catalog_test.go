package catalog

import (
	"strings"
	"testing"
)

func TestPlainTextRejectsActiveMarkupAndKeepsParagraphs(t *testing.T) {
	got := PlainText(`<p>Magic &amp; mystery</p><script>alert(1)</script><style>secret</style><iframe>remote</iframe><p>Second<br>line<img src="https://example.test/pixel"></p>`)
	if got != "Magic & mystery\nSecond\nline" {
		t.Fatal(got)
	}
	if NameKey("ÉLÉMENTAL") != NameKey("elemental") {
		t.Fatal("name ordering ignored accent/case folding")
	}
	if ReleaseDay("2024-02-30") != "" || ReleaseDay("unknown") != "" || ReleaseDay("2024-02-29T23:00:00-08:00") != "2024-02-29" {
		t.Fatal("release dates guessed or shifted calendar days")
	}
	if !strings.HasPrefix("élève", Limit("élève", 1)) {
		t.Fatal("UTF-8 split")
	}
}
