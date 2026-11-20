package wechatdraft

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/gif"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func gifFixture(t *testing.T, frames int) []byte {
	t.Helper()
	animation := &gif.GIF{LoopCount: 3}
	for i := 0; i < frames; i++ {
		frame := image.NewPaletted(image.Rect(0, 0, 8, 8), color.Palette{color.Transparent, color.Black})
		frame.SetColorIndex(i%8, i%8, 1)
		animation.Image = append(animation.Image, frame)
		animation.Delay = append(animation.Delay, 5+i)
	}
	var output bytes.Buffer
	if err := gif.EncodeAll(&output, animation); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func TestGIFBudgetsAndMultipartPreserveAnimation(t *testing.T) {
	data := gifFixture(t, 3)
	if err := validateGIF(bytes.NewReader(data)); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range [][]byte{gifFixture(t, 201), data[:20], append(bytes.Clone(data), 0), append(bytes.Clone(data), make([]byte, 1_000_000)...)} {
		if err := validateGIF(bytes.NewReader(invalid)); err == nil {
			t.Fatal("accepted invalid or unbounded GIF")
		}
	}
	// Frame dimensions are checked before DecodeAll can allocate those pixels.
	oversized := bytes.Clone(data)
	for i := 13; i+9 < len(oversized); i++ {
		if oversized[i] == 0x2c {
			oversized[i+5], oversized[i+6], oversized[i+7], oversized[i+8] = 0xff, 0xff, 0xff, 0xff
			break
		}
	}
	if boundedGIFFrames(oversized) {
		t.Fatal("accepted excessive frame pixel budget")
	}
	path := filepath.Join(t.TempDir(), "background.gif")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	format, err := decodeImage(path)
	if err != nil || format != "gif" {
		t.Fatal("GIF decoder failed", err)
	}
	body, contentType, failure := materialBody(context.Background(), path, "/cgi-bin/media/uploadimg", false)
	if failure != nil {
		t.Fatal(failure)
	}
	defer body.Close()
	encoded, err := io.ReadAll(body)
	if err != nil || int64(len(encoded)) != body.ContentLength() || !bytes.Contains(encoded, data) || !bytes.Contains(encoded, []byte(`filename="background.gif"`)) || contentType == "" {
		t.Fatal("GIF upload bytes or filename changed", err)
	}
}
