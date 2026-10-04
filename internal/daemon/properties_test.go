package daemon

import "testing"

// 规格表是靠人往里加字段的，最容易出的错不是逻辑写错，而是「新加了一项但忘了填 Group」——
// 那样界面会多出一个没有名字的分组，而且没有任何报错。用测试把它变成编译期之外的第二道闸。
func TestPropSpecsComplete(t *testing.T) {
	seen := map[string]bool{}
	for i, s := range propSpecs {
		if s.Key == "" {
			t.Errorf("propSpecs[%d] 缺少 Key", i)
		}
		if seen[s.Key] {
			t.Errorf("propSpecs[%d] Key %q 重复", i, s.Key)
		}
		seen[s.Key] = true

		if s.Group == "" {
			t.Errorf("%s 缺少 Group —— 界面上会出现一个没有名字的分组", s.Key)
		}
		if s.Label == "" {
			t.Errorf("%s 缺少 Label", s.Key)
		}
		switch s.Kind {
		case "string", "int", "bool":
		case "enum":
			if len(s.Options) == 0 {
				t.Errorf("%s 是 enum 但没给 Options", s.Key)
			}
		default:
			t.Errorf("%s 的 Kind %q 不认识", s.Key, s.Kind)
		}
		if s.Value == "" && s.Kind != "string" && s.Kind != "enum" {
			t.Errorf("%s 是非字符串类型但没给默认值", s.Key)
		}
	}
}

func TestValidateProp(t *testing.T) {
	cases := []struct {
		key, val string
		wantErr  bool
	}{
		{"max-players", "20", false},
		{"max-players", "abc", true},
		{"max-players", "", true},
		{"online-mode", "true", false},
		{"online-mode", "TRUE", false},
		{"online-mode", "yes", true},
		{"difficulty", "hard", false},
		{"difficulty", "hell", true},
		// 插件往 server.properties 里塞的自定义键也要能写，不能因为「不认识」就拒收
		{"some-plugin-key", "whatever", false},
		{"motd", "两行\n标语", true},
	}
	for _, c := range cases {
		err := validateProp(c.key, c.val)
		if (err != nil) != c.wantErr {
			t.Errorf("validateProp(%q, %q) 错误 = %v，期望出错 = %v", c.key, c.val, err, c.wantErr)
		}
	}
}
