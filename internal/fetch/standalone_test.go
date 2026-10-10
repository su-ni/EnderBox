package fetch

import (
	"bytes"
	"context"
	"crypto/sha512"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// FetchStandalone 是给整合包用的：借下载链路（分片/续传/重试/校验/原子落盘），
// 但**不占任务条**。这几条测试盯的就是「链路真的用上了」和「真的没占任务条」两件事。

func TestFetchStandaloneDownloadsVerifiesAndStaysOutOfTaskList(t *testing.T) {
	data := testData(5 << 20) // 5 MB → 走多线程分片，和普通下载同一条路
	srv := httptest.NewServer(rangeHandler(data, true))
	defer srv.Close()

	m := NewManager()
	// 上一级目录故意不存在：分片是往目标目录写的，目录没建好时报的是
	// 「无法写入分片文件」而不是「落盘失败」，容易带偏排查方向
	dest := filepath.Join(t.TempDir(), "mods", "some-mod.jar")

	var bytesSeen int64
	done := make(chan error, 1)
	go func() {
		done <- m.FetchStandalone(context.Background(), StandaloneOpts{
			URL: srv.URL, Dest: dest,
			WantSHA: sha256Of(data), SHAKind: "sha256", Size: int64(len(data)),
			OnDelta: func(d int64) { atomic.AddInt64(&bytesSeen, d) },
		})
	}()

	// ⚠ 这是本文件最要紧的一条判据：**下载过程中任务条里也不该出现它**。
	// 整合包一次几十上百个文件，漏进去一条就是几十上百条。
	leaked := 0
	for {
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("下载失败：%v", err)
			}
			if got := m.List(""); len(got) != 0 {
				t.Errorf("任务条里出现了 %d 条 —— 隐藏任务没被过滤掉", len(got))
			}
			if leaked > 0 {
				t.Errorf("下载过程中有 %d 次在任务条里看到了隐藏任务", leaked)
			}
			out, rerr := os.ReadFile(dest)
			if rerr != nil {
				t.Fatalf("落盘的文件读不到：%v", rerr)
			}
			if !bytes.Equal(out, data) {
				t.Error("内容与源不一致")
			}
			if _, err := os.Stat(dest + ".part"); err == nil {
				t.Error("留下了 .part 临时文件")
			}
			// 字节进度要能报出来，且所有字节恰好各报一次 ——
			// 报少了进度条会永远差一截并停在 99%
			if bytesSeen != int64(len(data)) {
				t.Errorf("累计报出的字节 = %d，期望 %d", bytesSeen, len(data))
			}
			return
		case <-time.After(20 * time.Millisecond):
			if len(m.List("")) > 0 {
				leaked++
			}
		}
	}
}

// 校验不通过时必须判失败，并且**把坏文件删掉** —— 留着一个哈希不对的模组，
// 服务端起来时才会报错，那时用户已经完全想不到是下载环节的问题。
func TestFetchStandaloneRejectsBadChecksum(t *testing.T) {
	data := testData(3 << 20)
	srv := httptest.NewServer(rangeHandler(data, true))
	defer srv.Close()

	m := NewManager()
	dest := filepath.Join(t.TempDir(), "mods", "bad.jar")
	err := m.FetchStandalone(context.Background(), StandaloneOpts{
		URL: srv.URL, Dest: dest,
		WantSHA: strings.Repeat("0", 64), SHAKind: "sha256", Size: int64(len(data)),
	})
	if err == nil {
		t.Fatal("校验和不匹配却成功了")
	}
	if !strings.Contains(err.Error(), "校验") {
		t.Errorf("错误信息要说清是校验问题：%v", err)
	}
	if _, serr := os.Stat(dest); serr == nil {
		t.Error("坏文件被留在目标路径上了 —— 用户会拿它去开服")
	}
	if _, serr := os.Stat(dest + ".part"); serr == nil {
		t.Error("留下了 .part 临时文件")
	}
	if len(m.List("")) != 0 {
		t.Error("失败后任务条里还残留着隐藏任务")
	}
}

// Modrinth 索引里的模组哈希清一色 sha512。这里不认 sha512 的话校验会全按 sha256 算，
// 整合包升级时**每一个模组都过不了校验** —— 这条钉的就是那个坑。
func TestFetchStandaloneVerifiesSHA512(t *testing.T) {
	data := testData(64 << 10)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(data)
	}))
	defer srv.Close()

	m := NewManager()
	dest := filepath.Join(t.TempDir(), "mods", "some-mod.jar")
	sum := sha512.Sum512(data)
	if err := m.FetchStandalone(context.Background(), StandaloneOpts{
		URL: srv.URL, Dest: dest,
		WantSHA: hex.EncodeToString(sum[:]), SHAKind: "sha512", Size: int64(len(data)),
	}); err != nil {
		t.Fatalf("sha512 校验下不该失败：%v", err)
	}
	// 反向：哈希对不上必须报校验失败，不能静默放行
	if err := m.FetchStandalone(context.Background(), StandaloneOpts{
		URL: srv.URL, Dest: dest + "2",
		WantSHA: strings.Repeat("f", 128), SHAKind: "sha512", Size: int64(len(data)),
	}); err == nil {
		t.Fatal("sha512 对不上却通过了")
	}
}

// 包里偶尔有文件没给下载地址（作者漏了、平台撤了）。这时要**如实报错并说清是哪一类问题**，
// 让上层的 Failed 清单能指向具体文件 —— 静默跳过会让用户对着「少了一个模组」莫名其妙。
func TestFetchStandaloneExplainsMissingURL(t *testing.T) {
	m := NewManager()
	err := m.FetchStandalone(context.Background(), StandaloneOpts{
		URL: "  ", Dest: filepath.Join(t.TempDir(), "x.jar"),
	})
	if err == nil {
		t.Fatal("没有下载地址却成功了")
	}
	if !strings.Contains(err.Error(), "下载地址") {
		t.Errorf("错误信息要提到「下载地址」：%v", err)
	}
}
