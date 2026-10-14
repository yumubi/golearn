// Command demo1：方法表达式的可运行证据（正文 §2 实测行引用）。
//
// 运行: go run ./exercises/p30/cmd/demo1
// 输出: 3 3 / func(*p30iface.T, int) / func(p30iface.T) int
package main

import "fmt"

type T struct{ n int }

func (t T) Get() int   { return t.n } // 等价于 func Get(t T) int
func (t *T) Set(a int) { t.n = a }    // 等价于 func Set(t *T, a int) int

func main() {
	f1 := (*T).Set // 方法表达式：类型 func(*T, int)
	f2 := T.Get    // 方法表达式：类型 func(T) int
	fmt.Printf("%T\n%T\n", f1, f2)

	var t T
	f1(&t, 3)
	fmt.Println(f2(t), t.n) // 3 3：方法与函数行为一致
}
