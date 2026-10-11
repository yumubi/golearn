package pkg

// NotFoundError 练习用自定义错误。
// 练习目标：理解 error 是接口，任何类型实现 Error() string 就是 error。
// （这里预置类型定义，练习者在 error_test.go 里实现 lookup 并 errors.As 取回它。）
type NotFoundError struct{ Key string }

func (e *NotFoundError) Error() string { return "key not found: " + e.Key }
