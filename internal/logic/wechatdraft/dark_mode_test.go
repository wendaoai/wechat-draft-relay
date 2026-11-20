package wechatdraft

import (
	"strings"
	"testing"

	v1 "github.com/wendaoai/wechat-draft-relay/api/wechatdraft/v1"
)

func TestDarkModeOptOutSurvivesRelayRendering(t *testing.T) {
	source := `<section data-no-dark="" style="background:#f7f7f5"><section data-no-dark="" style="display:grid;grid-template-columns:minmax(0,1fr)"><p data-no-dark="" data-ignore-dm="low-contrast" style="grid-area:1 / 1;color:#62666b">正文<span data-no-dark="" style="color:#aeb4b8">小字</span></p><img data-no-dark="" src="cw-image://art"></section></section>`
	images := []v1.Image{{ID: "art", Role: "body"}}
	prepared, fault := ProcessHTML(source, images, nil)
	if fault != nil || prepared != source {
		t.Fatalf("snapshot changed: %v", fault)
	}
	final, fault := ProcessHTML(prepared, images, map[string]string{"art": "https://mmbiz.qpic.cn/art.png"})
	if fault != nil {
		t.Fatal(fault)
	}
	if strings.Count(final, `data-no-dark=""`) != 5 || !strings.Contains(final, `data-ignore-dm="low-contrast"`) || !strings.Contains(final, "grid-area:1 / 1") || !strings.Contains(final, "https://mmbiz.qpic.cn/art.png") {
		t.Fatalf("lost color or layout contract: %s", final)
	}
	if _, fault := ProcessHTML(strings.Replace(source, `data-ignore-dm="low-contrast"`, `onclick="bad"`, 1), images, nil); fault == nil {
		t.Fatal("allowing dark-mode attributes must not allow executable attributes")
	}
}
