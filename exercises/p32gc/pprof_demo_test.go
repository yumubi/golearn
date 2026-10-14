package p32gc

import "testing"

// TestPPROFDemo：四级函数逐一等价性（正文 §5）。
// PREDICT：0..9999 过滤 %1000==0 应得 10 个（0 也是 1000 的倍数）。
func TestPPROFDemo(t *testing.T) {
	for _, f := range []func() []int{recall1, recall2, recall3, recall4} {
		r := f()
		if len(r) != 10000 || r[9999] != -1 || r[0] != 0 {
			t.Fatalf("%T 输出异常", r)
		}
	}
	if got := Search(); len(got) != 10 || got[0] != 0 {
		t.Fatalf("Search 过滤结果异常: %v", got)
	}
}
