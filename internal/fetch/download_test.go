package fetch

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// 分片方案最要紧的一条：**不多不少刚好盖满整个文件**。
// 盖不满 → 拼出来的文件短一截；盖超了 → 分片文件比它该有的长，拼接时错位。
// 两种都是「校验不通过」，但根因在这里，所以直接在这儿测死。
func TestPlanSegmentsCoversWholeFileExactly(t *testing.T) {
	totals := []int64{
		segMinSize, // 刚好到门槛
		3<<20 + 7,  // 除不尽
		8837089,    // 实测的 Forge 安装器大小
		50 << 20,   // 一个 Paper jar
		200 << 20,  // 大文件，线程数应该被 segMaxParts 卡住
	}
	for _, total := range totals {
		parts := planSegments(total)
		if len(parts) < 2 {
			t.Fatalf("total=%d 切出 %d 片，2 MB 以上至少该有 2 片", total, len(parts))
		}
		if len(parts) > segMaxParts {
			t.Errorf("total=%d 切出 %d 片，超过上限 %d", total, len(parts), segMaxParts)
		}
		var at int64
		for i, p := range parts {
			if p.start != at {
				t.Errorf("total=%d 第 %d 片从 %d 开始，期望 %d（中间断档或重叠）", total, i, p.start, at)
			}
			if p.len() <= 0 {
				t.Errorf("total=%d 第 %d 片长度是 %d", total, i, p.len())
			}
			at = p.end + 1
		}
		if at != total {
			t.Errorf("total=%d 只盖到 %d，差 %d 字节", total, at, total-at)
		}
	}
}

func TestTotalFromContentRange(t *testing.T) {
	cases := []struct {
		header string
		want   int64
	}{
		{"bytes 0-0/8837089", 8837089},
		{"bytes */8837089", 8837089}, // 416 那种形状
		{"bytes 100-199/1000", 1000},
		{"", 0},
		{"garbage", 0},
		{"bytes 0-0/", 0},
	}
	for _, c := range cases {
		r := &http.Response{Header: http.Header{}}
		if c.header != "" {
			r.Header.Set("Content-Range", c.header)
		}
		if got := totalFromContentRange(r); got != c.want {
			t.Errorf("totalFromContentRange(%q) = %d，期望 %d", c.header, got, c.want)
		}
	}
}

// 残留分片必须是「宁可重下，不可硬拼」。任何一处对不上都得判脏。
func TestSegFilesMismatchDetectsStale(t *testing.T) {
	dir := t.TempDir()
	part := filepath.Join(dir, "server.jar.part")
	const total = int64(4 << 20) // 4 片，每片 1 MB

	if segFilesMismatch(part, total) {
		t.Fatal("一片分片文件都没有时，不该算脏（那是全新下载）")
	}

	for i := 0; i < 4; i++ {
		if err := os.WriteFile(segPath(part, i), make([]byte, 1<<20), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if segFilesMismatch(part, total) {
		t.Fatal("尺寸正确的 4 片不该算脏")
	}

	// 多出一片：残留的是上一次别的分片方案的
	if err := os.WriteFile(segPath(part, 4), make([]byte, 16), 0o644); err != nil {
		t.Fatal(err)
	}
	if !segFilesMismatch(part, total) {
		t.Error("片数对不上该算脏")
	}
	_ = os.Remove(segPath(part, 4))

	// 某一片比它该有的长度还大：上游换过文件
	if err := os.WriteFile(segPath(part, 1), make([]byte, 2<<20), 0o644); err != nil {
		t.Fatal(err)
	}
	if !segFilesMismatch(part, total) {
		t.Error("分片尺寸超标该算脏")
	}
}

func TestBackoffDelayGivesUpAtEightSeconds(t *testing.T) {
	cases := []struct {
		attempt int
		want    time.Duration
	}{
		{1, 0},
		{2, time.Second},
		{3, 2 * time.Second},
		{4, 4 * time.Second},
		{7, 8 * time.Second}, // 别无限翻倍，用户就坐在屏幕前面看着
	}
	for _, c := range cases {
		if got := backoffDelay(c.attempt); got != c.want {
			t.Errorf("backoffDelay(%d) = %v，期望 %v", c.attempt, got, c.want)
		}
	}
}

func TestClip(t *testing.T) {
	if got := clip("abcdef", 3); got != "abc" {
		t.Errorf("clip 超长 = %q", got)
	}
	if got := clip("ab", 3); got != "ab" {
		t.Errorf("clip 不超长应原样返回，拿到 %q", got)
	}
}
