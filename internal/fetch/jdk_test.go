package fetch

import "testing"

// 这些 HTML 片段是 2026-10-04 从真实索引页上复制下来的原样文本。
// 为什么要留真样本：文件名规则是从源站实况总结的，而源站随时可能改；
// 只留自己编的字符串，测试会在规则悄悄失效时继续绿着。
//
// 三处勘误都在这里钉住了：
//   · 版本段不能写成 \d+\.\d+，Java 8 是 8u504b01
//   · 同一份索引页里既有 .zip 也有 .msi(aarch64)，必须只取归档格式
//   · mac 同时有 .pkg 和 .tar.gz

const (
	sampleNJU21Win = `<a href="?C=N;O=D">Name</a>
<a href="OpenJDK21U-jdk_x64_windows_hotspot_21.0.12.1_1.msi">OpenJDK21U-jdk_x64_windows_hotspot_21.0.12.1_1.msi</a>
<a href="OpenJDK21U-jdk_x64_windows_hotspot_21.0.12.1_1.zip">OpenJDK21U-jdk_x64_windows_hotspot_21.0.12.1_1.zip</a>
<a href="https://www.yaoge123.com">mirror</a>`

	sampleUSTC21Win = `<a href="/adoptium/releases/temurin21-binaries/LatestRelease/OpenJDK21U-jdk_x64_windows_hotspot_21.0.9_10.zip.sha256.txt">x</a>
<a href="/adoptium/releases/temurin21-binaries/LatestRelease/OpenJDK21U-jdk_x64_windows_hotspot_21.0.9_10.zip">zip</a>
<a href="/adoptium/releases/temurin21-binaries/LatestRelease/OpenJDK21U-jdk_aarch64_windows_hotspot_21.0.9_10.zip">zip</a>
<a href="/adoptium/releases/temurin21-binaries/LatestRelease/OpenJDK21U-debugimage_x64_windows_hotspot_21.0.9_10.zip">zip</a>`

	sampleJava8 = `<a href="OpenJDK8U-jdk_x64_windows_hotspot_8u504b01.msi">msi</a>
<a href="OpenJDK8U-jdk_x64_windows_hotspot_8u504b01.zip">zip</a>`

	sampleLinux = `<a href="OpenJDK21U-jdk_x64_linux_hotspot_21.0.12.1_1.tar.gz">tar.gz</a>`

	sampleMac = `<a href="OpenJDK21U-jdk_x64_mac_hotspot_21.0.12.1_1.pkg">pkg</a>
<a href="OpenJDK21U-jdk_x64_mac_hotspot_21.0.12.1_1.tar.gz">tar.gz</a>`
)

func TestJDKFileRe(t *testing.T) {
	cases := []struct {
		name, arch, os, ext string
		body                string
		wantFile            string
		wantVer             string
	}{
		{"南大 win21", "x64", "windows", ".zip", sampleNJU21Win,
			"OpenJDK21U-jdk_x64_windows_hotspot_21.0.12.1_1.zip", "21.0.12.1_1"},
		{"中科大 win21", "x64", "windows", ".zip", sampleUSTC21Win,
			"OpenJDK21U-jdk_x64_windows_hotspot_21.0.9_10.zip", "21.0.9_10"},
		{"Java 8 的老式版本写法", "x64", "windows", ".zip", sampleJava8,
			"OpenJDK8U-jdk_x64_windows_hotspot_8u504b01.zip", "8u504b01"},
		{"linux tar.gz", "x64", "linux", ".tar.gz", sampleLinux,
			"OpenJDK21U-jdk_x64_linux_hotspot_21.0.12.1_1.tar.gz", "21.0.12.1_1"},
		{"mac 要跳过 pkg", "x64", "mac", ".tar.gz", sampleMac,
			"OpenJDK21U-jdk_x64_mac_hotspot_21.0.12.1_1.tar.gz", "21.0.12.1_1"},
		{"aarch64 只认自己那份", "aarch64", "windows", ".zip", sampleUSTC21Win,
			"OpenJDK21U-jdk_aarch64_windows_hotspot_21.0.9_10.zip", "21.0.9_10"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := jdkFileRe(c.arch, c.os, c.ext).FindStringSubmatch(c.body)
			if m == nil {
				t.Fatalf("没匹配到（%s/%s/%s）", c.arch, c.os, c.ext)
			}
			if m[1] != c.wantFile {
				t.Errorf("文件名不对\n 期望 %s\n 实际 %s", c.wantFile, m[1])
			}
			if m[2] != c.wantVer {
				t.Errorf("版本段不对\n 期望 %s\n 实际 %s", c.wantVer, m[2])
			}
		})
	}
}

// debugimage / jre / testimage 这些同前缀条目必须永远不被当成 jdk 归档。
//
// 为什么单列一个测试：它们的文件名里也带着 "x64_windows_hotspot"，
// 只按「SDK 有名的那一段」去匹配就会把它们选中 —— 结果是下一个几百 MB 的
// debugimage 下来，解压完找不到 bin/java。这类失败在 download 阶段完全看不出来，
// 要等到安装阶段才炸，属于最难查的一档。
func TestJDKFileReIgnoresNonJDKArtifacts(t *testing.T) {
	samples := map[string]string{
		"debugimage": `<a href="OpenJDK21U-debugimage_x64_windows_hotspot_21.0.9_10.zip">x</a>`,
		"jre":        `<a href="OpenJDK21U-jre_x64_windows_hotspot_21.0.9_10.zip">x</a>`,
		"testimage":  `<a href="OpenJDK21U-testimage_x64_windows_hotspot_21.0.9_10.zip">x</a>`,
		"static-jre": `<a href="OpenJDK21U-static-jre_x64_windows_hotspot_21.0.9_10.zip">x</a>`,
	}
	for name, body := range samples {
		if m := jdkFileRe("x64", "windows", ".zip").FindStringSubmatch(body); m != nil {
			t.Errorf("%s 被误认成了 jdk 归档：%s", name, m[1])
		}
	}
}

func TestPrettyJDKVersion(t *testing.T) {
	cases := [][2]string{
		{"21.0.12.1_1", "21.0.12.1+1"}, // 新版本号：下划线就是 Adoptium 的加号
		{"17.0.9_9", "17.0.9+9"},
		{"25.0.4.1_1", "25.0.4.1+1"},
		{"8u504b01", "8u504b01"}, // 老式写法保持原样，别硬套加号
	}
	for _, c := range cases {
		if got := prettyJDKVersion(c[0]); got != c[1] {
			t.Errorf("prettyJDKVersion(%q) = %q，期望 %q", c[0], got, c[1])
		}
	}
}
